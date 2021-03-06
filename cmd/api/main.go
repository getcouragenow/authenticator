// Command API exposes user authentication HTTP API.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/go-redis/redis/v8"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/oklog/run"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"

	auth "github.com/fmitra/authenticator"
	"github.com/fmitra/authenticator/internal/contactapi"
	"github.com/fmitra/authenticator/internal/deviceapi"
	"github.com/fmitra/authenticator/internal/httpapi"
	"github.com/fmitra/authenticator/internal/loginapi"
	"github.com/fmitra/authenticator/internal/mail"
	"github.com/fmitra/authenticator/internal/msgconsumer"
	"github.com/fmitra/authenticator/internal/msgpublisher"
	"github.com/fmitra/authenticator/internal/msgrepo"
	"github.com/fmitra/authenticator/internal/otp"
	"github.com/fmitra/authenticator/internal/password"
	"github.com/fmitra/authenticator/internal/postgres"
	"github.com/fmitra/authenticator/internal/sendgrid"
	"github.com/fmitra/authenticator/internal/signupapi"
	"github.com/fmitra/authenticator/internal/token"
	"github.com/fmitra/authenticator/internal/tokenapi"
	"github.com/fmitra/authenticator/internal/totpapi"
	"github.com/fmitra/authenticator/internal/twilio"
	"github.com/fmitra/authenticator/internal/webauthn"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	var err error
	var logger log.Logger
	{
		logger = log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
		logger = log.With(logger, "ts", log.DefaultTimestampUTC)
		logger = log.With(logger, "caller", log.DefaultCaller)
	}

	var configPath string
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	{
		fs.Bool("api.debug", false, "Enable debug logging")
		fs.String("api.http-addr", ":8080", "Address to listen on")
		fs.String("api.allowed-origins", "*", "Comma separated list of allowed origins")
		fs.String("api.cookie-domain", "", "Domain to set HTTP cookie")
		fs.Int("api.cookie-max-age", 605800, "Max age of cookie, in seconds")
		fs.String("pg.conn-string", "", "Postgres connection string")
		fs.String("redis.conn-string", "", "Redis connection string")
		fs.Int("password.min-length", 8, "Minimum password length")
		fs.Int("password.max-length", 1000, "Maximum password length")
		fs.Int("otp.code-length", 6, "OTP code length")
		fs.String("otp.issuer", "", "TOTP issuer domain")
		fs.String("otp.secret.key", "", "Encryption key for TOTP secrets")
		fs.Int("otp.secret.version", 1, "Current version of encryption key")
		fs.Int("msgconsumer.workers", 4, "Total number of workers to process outgoing messages")
		fs.Duration("token.expires-in", time.Minute*20, "JWT token expiry time")
		fs.Duration("token.refresh-expires-in", time.Hour*24*15, "Refresh token expiry time")
		fs.String("token.issuer", "authenticator", "JWT token issuer")
		fs.String("token.secret", "", "JWT token secret")
		fs.Int("webauthn.max-devices", 5, "Maximum amount of devices for registration")
		fs.String("webauthn.display-name", "Authenticator", "Webauthn display name")
		fs.String("webauthn.domain", "authenticator.local", "Public client domain")
		fs.String("webauthn.request-origin", "authenticator.local", "Origin URL for client requests")
		fs.String("twilio.account-sid", "", "Account SID from Twilio")
		fs.String("twilio.token", "", "Authentication token for Twilio API")
		fs.String("twilio.sms-sender", "", "Origin phone number for outgoing SMS")
		fs.String("mail.server-addr", "", "Outgoing mail server")
		fs.String("mail.from-addr", "", "Origin email address for outgoing email")
		fs.String("mail.auth.username", "", "Username for mailing service")
		fs.String("mail.auth.password", "", "Password for mailing service")
		fs.String("mail.auth.hostname", "", "Hostname for mailing service")
		fs.String("sendgrid.api-key", "", "Sendgrid API Key for mailing services")
		fs.String("sendgrid.from-addr", "", "Origin email address for outgoing email")
		fs.String("sendgrid.from-name", "", "Origin name for outgoing email")
		fs.String("maillib", "", "Email library to use. If not set, it will us net/smtp")

		fs.StringVar(&configPath, "config", "", "Path to the config file")
		err = fs.Parse(os.Args[1:])
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		if err != nil {
			logger.Log("message", "failed to parse cli flags", "error", err, "source", "cmd/api")
			os.Exit(1)
		}
	}

	if _, err = os.Stat(configPath); !os.IsNotExist(err) {
		viper.SetConfigFile(configPath)
		err = viper.ReadInConfig()
		if err != nil {
			logger.Log("message", "failed to load config file", "error", err, "source", "cmd/api")
			os.Exit(1)
		}
	}
	if err = viper.BindPFlags(fs); err != nil {
		logger.Log("message", "failed to load cli flags", "error", err, "source", "cmd/api")
		os.Exit(1)
	}

	if viper.GetBool("api.debug") {
		logger.Log("message", "enabling debug messaging", "source", "cmd/api")
		logger = level.NewFilter(logger, level.AllowDebug())
	} else {
		logger.Log("message", "debug messaging is disabled", "source", "cmd/api")
		logger = level.NewFilter(logger, level.AllowInfo())
	}

	passwordSvc := password.NewPassword(
		password.WithMinLength(viper.GetInt("password.min-length")),
		password.WithMaxLength(viper.GetInt("password.max-length")),
	)

	var pgDB *sql.DB
	{
		pgDB, err = sql.Open("postgres", viper.GetString("pg.conn-string"))
		if err != nil {
			logger.Log(
				"message", "postgres connection failed",
				"error", err,
				"source", "cmd/api",
			)
			os.Exit(1)
		}
		if err = pgDB.Ping(); err != nil {
			logger.Log("message", "postgres did not respond", "error", err, "source", "cmd/api")
			os.Exit(1)
		}
		defer func() {
			if err = pgDB.Close(); err != nil {
				logger.Log(
					"message", "failed to close postgres connection",
					"error", err,
					"source", "cmd/api",
				)
			}
		}()
	}

	var redisDB *redis.Client
	{
		redisConf, err := redis.ParseURL(viper.GetString("redis.conn-string"))
		if err != nil {
			logger.Log("message", "invalid redis configuration", "error", err, "source", "cmd/api")
			os.Exit(1)
		}
		redisDB = redis.NewClient(redisConf)
		closeRedis := func() {
			if err = redisDB.Close(); err != nil {
				logger.Log(
					"message", "failed to close redis connection",
					"error", err,
					"source", "cmd/api",
				)
			}
		}

		if _, err = redisDB.Ping(ctx).Result(); err != nil {
			logger.Log("message", "redis connection failed", "error", err, "source", "cmd/api")
			closeRedis()
			os.Exit(1)
		}
		defer closeRedis()
	}

	messageRepo := msgrepo.NewService(msgrepo.WithLogger(logger))

	repoMngr := postgres.NewClient(
		postgres.WithLogger(logger),
		postgres.WithPassword(passwordSvc),
		postgres.WithDB(pgDB),
	)

	otpSvc := otp.NewOTP(
		otp.WithCodeLength(viper.GetInt("otp.code-length")),
		otp.WithIssuer(viper.GetString("otp.issuer")),
		otp.WithSecret(otp.Secret{
			Key:     viper.GetString("otp.secret.key"),
			Version: viper.GetInt("otp.secret.version"),
		}),
		otp.WithDB(redisDB),
	)

	messagingSvc := msgpublisher.NewService(messageRepo, msgpublisher.WithLogger(logger))

	tokenSvc := token.NewService(
		token.WithLogger(logger),
		token.WithDB(redisDB),
		token.WithTokenExpiry(viper.GetDuration("token.expires-in")),
		token.WithRefreshTokenExpiry(viper.GetDuration("token.refresh-expires-in")),
		token.WithIssuer(viper.GetString("token.issuer")),
		token.WithSecret(viper.GetString("token.secret")),
		token.WithOTP(otpSvc),
		token.WithCookieMaxAge(viper.GetInt("api.cookie-max-age")),
		token.WithCookieDomain(viper.GetString("api.cookie-domain")),
		token.WithRepoManager(repoMngr),
	)

	webauthnSvc, err := webauthn.NewService(
		webauthn.WithDB(redisDB),
		webauthn.WithDisplayName(viper.GetString("webauthn.display-name")),
		webauthn.WithDomain(viper.GetString("webauthn.domain")),
		webauthn.WithRequestOrigin(viper.GetString("webauthn.request-origin")),
		webauthn.WithRepoManager(repoMngr),
		webauthn.WithMaxDevices(viper.GetInt("webauthn.max-devices")),
	)
	if err != nil {
		logger.Log("message", "failed to build webauthn service", "error", err, "source", "cmd/api")
		os.Exit(1)
	}

	loginAPI := loginapi.NewService(
		loginapi.WithLogger(logger),
		loginapi.WithTokenService(tokenSvc),
		loginapi.WithRepoManager(repoMngr),
		loginapi.WithWebAuthn(webauthnSvc),
		loginapi.WithOTP(otpSvc),
		loginapi.WithMessaging(messagingSvc),
		loginapi.WithPassword(passwordSvc),
	)

	signupAPI := signupapi.NewService(
		signupapi.WithLogger(logger),
		signupapi.WithTokenService(tokenSvc),
		signupapi.WithRepoManager(repoMngr),
		signupapi.WithMessaging(messagingSvc),
		signupapi.WithOTP(otpSvc),
	)

	deviceAPI := deviceapi.NewService(
		deviceapi.WithLogger(logger),
		deviceapi.WithWebAuthn(webauthnSvc),
		deviceapi.WithRepoManager(repoMngr),
		deviceapi.WithTokenService(tokenSvc),
	)

	contactAPI := contactapi.NewService(
		contactapi.WithLogger(logger),
		contactapi.WithOTP(otpSvc),
		contactapi.WithRepoManager(repoMngr),
		contactapi.WithMessaging(messagingSvc),
		contactapi.WithTokenService(tokenSvc),
	)

	totpAPI := totpapi.NewService(
		totpapi.WithLogger(logger),
		totpapi.WithOTP(otpSvc),
		totpapi.WithRepoManager(repoMngr),
		totpapi.WithTokenService(tokenSvc),
	)

	tokenAPI := tokenapi.NewService(
		tokenapi.WithLogger(logger),
		tokenapi.WithTokenService(tokenSvc),
		tokenapi.WithRepoManager(repoMngr),
	)

	lmt := httpapi.NewRateLimiter(redisDB)
	router := mux.NewRouter()
	router.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	loginapi.SetupHTTPHandler(loginAPI, router, tokenSvc, logger, lmt)
	signupapi.SetupHTTPHandler(signupAPI, router, tokenSvc, logger, lmt)
	deviceapi.SetupHTTPHandler(deviceAPI, router, tokenSvc, logger, lmt)
	contactapi.SetupHTTPHandler(contactAPI, router, tokenSvc, logger, lmt)
	totpapi.SetupHTTPHandler(totpAPI, router, tokenSvc, logger, lmt)
	tokenapi.SetupHTTPHandler(tokenAPI, router, tokenSvc, logger, lmt)

	server := http.Server{
		Addr: viper.GetString("api.http-addr"),
		Handler: handlers.CORS(
			handlers.AllowedOrigins(strings.Split(
				viper.GetString("api.allowed-origins"), ","),
			),
			handlers.AllowedHeaders([]string{
				"X-Requested-With",
				"Content-Type",
				"Authorization",
			}),
			handlers.AllowCredentials(),
			handlers.AllowedMethods([]string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "HEAD"}),
		)(router),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	smsLib := twilio.NewClient(twilio.WithDefaults(
		viper.GetString("twilio.account-sid"),
		viper.GetString("twilio.token"),
		viper.GetString("twilio.sms-sender"),
	))

	sendGrid := sendgrid.NewClient(
		viper.GetString("sendgrid.api-key"),
		viper.GetString("sendgrid.from-addr"),
		viper.GetString("sendgrid.from-name"),
	)
	stdMailer := mail.NewService(mail.WithDefaults(
		viper.GetString("mail.server-addr"),
		viper.GetString("mail.from-addr"),
		smtp.PlainAuth(
			"",
			viper.GetString("mail.auth.username"),
			viper.GetString("mail.auth.password"),
			viper.GetString("mail.auth.hostname"),
		),
	))

	var emailLib auth.Emailer
	if viper.GetString("maillib") == "sendgrid" {
		emailLib = sendGrid
	} else {
		emailLib = stdMailer
	}

	msgd := msgconsumer.NewService(
		messageRepo,
		smsLib,
		emailLib,
		msgconsumer.WithWorkers(viper.GetInt("msgconsumer.workers")),
		msgconsumer.WithLogger(logger),
	)

	var g run.Group
	{
		g.Add(func() error {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			return fmt.Errorf("signal received: %v", <-sig)
		}, func(err error) {
			logger.Log("message", "program was interrupted", "error", err, "source", "cmd/api")
			cancel()
		})
	}
	{
		g.Add(func() error {
			logger.Log(
				"message", "message daemon is starting to check messages",
				"source", "cmd/api",
			)
			return msgd.Run(ctx)
		}, func(err error) {
			logger.Log(
				"message", "message daemon was shut down",
				"error", err,
				"source", "cmd/api",
			)
		})
	}
	{
		g.Add(func() error {
			logger.Log(
				"message", "API server is starting",
				"address", server.Addr,
				"source", "cmd/api",
			)
			return server.ListenAndServe()
		}, func(err error) {
			logger.Log(
				"message", "API server was interrupted",
				"error", err,
				"source", "cmd/api",
			)
			logger.Log(
				"message", "API server shut down",
				"error", server.Shutdown(ctx),
				"source", "cmd/api",
			)
		})
	}

	err = g.Run()
	logger.Log("message", "actors stopped", "error", err, "source", "cmd/api")
}
