package signupapi

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"

	auth "github.com/fmitra/authenticator"
	"github.com/fmitra/authenticator/internal/httpapi"
	"github.com/fmitra/authenticator/internal/otp"
	"github.com/fmitra/authenticator/internal/postgres"
	"github.com/fmitra/authenticator/internal/test"
)

func TestSignUpAPI_SignUp(t *testing.T) {
	tt := []struct {
		name            string
		statusCode      int
		errMessage      string
		reqBody         []byte
		userCreateCalls int
		messagingCalls  int
		userGetFn       func() (*auth.User, error)
		userCreateFn    func() error
		tokenCreateFn   func() (*auth.Token, error)
		tokenSignFn     func() (string, error)
	}{
		{
			name:       "User query failure",
			statusCode: http.StatusInternalServerError,
			errMessage: "An internal error occurred",
			reqBody: []byte(`{
				"type": "email",
				"password": "swordfish",
				"identity": "jane@example.com"
			}`),
			userCreateCalls: 0,
			messagingCalls:  0,
			userGetFn: func() (*auth.User, error) {
				return nil, fmt.Errorf("database connection error")
			},
			userCreateFn: func() error {
				return nil
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return &auth.Token{Code: "123456"}, nil
			},
			tokenSignFn: func() (string, error) {
				return "jwt-token", nil
			},
		},
		{
			name:       "User already verified",
			statusCode: http.StatusBadRequest,
			errMessage: "Cannot register user",
			reqBody: []byte(`{
				"type": "email",
				"password": "swordfish",
				"identity": "jane@example.com"
			}`),
			userCreateCalls: 0,
			messagingCalls:  0,
			userGetFn: func() (*auth.User, error) {
				return &auth.User{IsVerified: true}, nil
			},
			userCreateFn: func() error {
				return nil
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return &auth.Token{Code: "123456"}, nil
			},
			tokenSignFn: func() (string, error) {
				return "jwt-token", nil
			},
		},
		{
			name:       "User creation failure",
			statusCode: http.StatusInternalServerError,
			errMessage: "An internal error occurred",
			reqBody: []byte(`{
				"type": "email",
				"password": "swordfish",
				"identity": "jane@example.com"
			}`),
			userCreateCalls: 1,
			messagingCalls:  0,
			userGetFn: func() (*auth.User, error) {
				return nil, sql.ErrNoRows
			},
			userCreateFn: func() error {
				return fmt.Errorf("faled to create user")
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return &auth.Token{Code: "123456"}, nil
			},
			tokenSignFn: func() (string, error) {
				return "jwt-token", nil
			},
		},
		{
			name:       "Token creation failure",
			statusCode: http.StatusInternalServerError,
			errMessage: "An internal error occurred",
			reqBody: []byte(`{
				"type": "email",
				"password": "swordfish",
				"identity": "jane@example.com"
			}`),
			userCreateCalls: 1,
			messagingCalls:  0,
			userGetFn: func() (*auth.User, error) {
				return nil, sql.ErrNoRows
			},
			userCreateFn: func() error {
				return nil
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return nil, fmt.Errorf("failed to create token")
			},
			tokenSignFn: func() (string, error) {
				return "jwt-token", nil
			},
		},
		{
			name:       "Token signing failure",
			statusCode: http.StatusInternalServerError,
			errMessage: "An internal error occurred",
			reqBody: []byte(`{
				"type": "email",
				"password": "swordfish",
				"identity": "jane@example.com"
			}`),
			userCreateCalls: 1,
			messagingCalls:  0,
			userGetFn: func() (*auth.User, error) {
				return nil, sql.ErrNoRows
			},
			userCreateFn: func() error {
				return nil
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return &auth.Token{Code: "123456"}, nil
			},
			tokenSignFn: func() (string, error) {
				return "", fmt.Errorf("failed to sign token")
			},
		},
		{
			name:            "Bad request body",
			statusCode:      http.StatusBadRequest,
			errMessage:      "Identity type must be email or phone",
			reqBody:         []byte(`{}`),
			userCreateCalls: 0,
			messagingCalls:  0,
			userGetFn: func() (*auth.User, error) {
				return nil, sql.ErrNoRows
			},
			userCreateFn: func() error {
				return nil
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return &auth.Token{Code: "123456"}, nil
			},
			tokenSignFn: func() (string, error) {
				return "jwt-token", nil
			},
		},
		{
			name:       "Successful request",
			statusCode: http.StatusCreated,
			errMessage: "",
			reqBody: []byte(`{
				"type": "email",
				"password": "swordfish",
				"identity": "jane@example.com"
			}`),
			userCreateCalls: 1,
			messagingCalls:  1,
			userGetFn: func() (*auth.User, error) {
				return nil, sql.ErrNoRows
			},
			userCreateFn: func() error {
				return nil
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return &auth.Token{
					CodeHash: test.MockTokenHash("", "", time.Now().Add(time.Minute*5).Unix()),
					State:    auth.JWTPreAuthorized,
				}, nil
			},
			tokenSignFn: func() (string, error) {
				return "jwt-token", nil
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			router := mux.NewRouter()
			logger := log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
			userRepo := &test.UserRepository{
				ByIdentityFn: tc.userGetFn,
				CreateFn:     tc.userCreateFn,
			}
			repoMngr := &test.RepositoryManager{
				UserFn: func() auth.UserRepository {
					return userRepo
				},
			}
			tokenSvc := &test.TokenService{
				CreateFn: tc.tokenCreateFn,
				SignFn:   tc.tokenSignFn,
			}
			messagingSvc := &test.MessagingService{}
			svc := NewService(
				WithLogger(&test.Logger{}),
				WithTokenService(tokenSvc),
				WithRepoManager(repoMngr),
				WithMessaging(messagingSvc),
			)

			req, err := http.NewRequest(
				"POST",
				"/api/v1/signup",
				bytes.NewBuffer(tc.reqBody),
			)
			if err != nil {
				t.Fatal("failed to create request:", err)
			}

			SetupHTTPHandler(svc, router, tokenSvc, logger, &httpapi.MockLimiterFactory{})

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != tc.statusCode {
				t.Errorf("incorrect status code, want %v got %v", tc.statusCode, rr.Code)
			}

			err = test.ValidateErrMessage(tc.errMessage, rr.Body)
			if err != nil {
				t.Error(err)
			}

			if repoMngr.Calls.NewWithTransaction != 0 {
				t.Errorf("incorrect RepositoryManager.NewWithTransaction() call count, want 0 got %v",
					repoMngr.Calls.NewWithTransaction)
			}

			if repoMngr.Calls.WithAtomic != 0 {
				t.Errorf("incorrect RepositoryManager.WithAtomic() call count, want 0 got %v",
					repoMngr.Calls.WithAtomic)
			}

			if userRepo.Calls.ReCreate != 0 {
				t.Errorf("incorrect UserRepository.ReCreate() call count, want 0 got %v",
					userRepo.Calls.ReCreate)
			}

			if userRepo.Calls.Create != tc.userCreateCalls {
				t.Errorf("incorrect UserRepository.Create() call count, want %v got %v",
					tc.userCreateCalls, userRepo.Calls.Create)
			}

			if messagingSvc.Calls.Send != tc.messagingCalls {
				t.Errorf("incorrect MessagingService.Send() call count, want %v got %v",
					tc.messagingCalls, messagingSvc.Calls.Send)
			}
		})
	}
}

func TestSignUpAPI_SignUpExistingUser(t *testing.T) {
	pgDB, err := test.NewPGDB()
	if err != nil {
		t.Fatal("failed to create test database:", err)
	}
	defer pgDB.DropDB()

	repoMngr := postgres.TestClient(pgDB.DB)

	ctx := context.Background()
	user := &auth.User{
		Password:  "swordfish",
		TFASecret: "tfa_secret",
		Email: sql.NullString{
			String: "jane@example.com",
			Valid:  true,
		},
		IsVerified: false,
	}
	err = repoMngr.User().Create(ctx, user)
	if err != nil {
		t.Fatal("failed to create uer:", err)
	}

	router := mux.NewRouter()
	logger := log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
	tokenSvc := &test.TokenService{
		CreateFn: func() (*auth.Token, error) {
			return &auth.Token{
				CodeHash: test.MockTokenHash("", "", time.Now().Add(time.Minute*5).Unix()),
				State:    auth.JWTPreAuthorized,
				Code:     test.OTPCode,
			}, nil
		},
		SignFn: func() (string, error) {
			return "jwt-token", nil
		},
	}
	messagingSvc := &test.MessagingService{}

	svc := NewService(
		WithLogger(&test.Logger{}),
		WithTokenService(tokenSvc),
		WithRepoManager(repoMngr),
		WithMessaging(messagingSvc),
	)

	req, err := http.NewRequest("POST", "/api/v1/signup", bytes.NewBuffer([]byte(`{
		"type": "email",
		"password": "swordfish",
		"identity": "jane@example.com"
	}`)))
	if err != nil {
		t.Fatal("failed to create request:", err)
	}

	SetupHTTPHandler(svc, router, tokenSvc, logger, &httpapi.MockLimiterFactory{})

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("incorrect status code, want %v got %v", http.StatusCreated, rr.Code)
	}

	newUser, err := repoMngr.User().ByIdentity(ctx, "Email", user.Email.String)
	if err != nil {
		t.Fatal("failed to retrieve user:", err)
	}

	if newUser.ID == user.ID {
		t.Error("user ID not reset on re-creation")
	}

	if messagingSvc.Calls.Send != 1 {
		t.Errorf("incorrect MessagingService.Send() call count, want 1 got %v",
			messagingSvc.Calls.Send)
	}
}

func TestSignUpAPI_VerifyCode(t *testing.T) {
	tt := []struct {
		name            string
		statusCode      int
		reqBody         []byte
		userFn          func() (*auth.User, error)
		messagingCalls  int
		tokenValidateFn func() (*auth.Token, error)
		tokenCreateFn   func() (*auth.Token, error)
		tokenSignFn     func() (string, error)
	}{
		{
			name:       "User query failure",
			statusCode: http.StatusInternalServerError,
			reqBody:    []byte(`{"code": "123456"}`),
			userFn: func() (*auth.User, error) {
				return nil, fmt.Errorf("whoops")
			},
			tokenValidateFn: func() (*auth.Token, error) {
				return &auth.Token{
					CodeHash: test.MockTokenHash("", "", time.Now().Add(time.Minute*5).Unix()),
					State:    auth.JWTPreAuthorized,
				}, nil
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return &auth.Token{}, nil
			},
			tokenSignFn: func() (string, error) {
				return "jwt-token", nil
			},
			messagingCalls: 0,
		},
		{
			name:       "Bad request failure",
			statusCode: http.StatusBadRequest,
			reqBody:    []byte(""),
			userFn: func() (*auth.User, error) {
				return &auth.User{IsEmailOTPAllowed: true}, nil
			},
			tokenValidateFn: func() (*auth.Token, error) {
				return &auth.Token{
					CodeHash: test.MockTokenHash("", "", time.Now().Add(time.Minute*5).Unix()),
					State:    auth.JWTPreAuthorized,
				}, nil
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return &auth.Token{}, nil
			},
			tokenSignFn: func() (string, error) {
				return "jwt-token", nil
			},
			messagingCalls: 0,
		},
		{
			name:       "Code invalid failure",
			statusCode: http.StatusBadRequest,
			reqBody:    []byte(`{"code": "222444"}`),
			userFn: func() (*auth.User, error) {
				return &auth.User{IsEmailOTPAllowed: true}, nil
			},
			tokenValidateFn: func() (*auth.Token, error) {
				return &auth.Token{
					CodeHash: test.MockTokenHash("", "", time.Now().Add(time.Minute*5).Unix()),
					State:    auth.JWTPreAuthorized,
				}, nil
			},
			tokenCreateFn: func() (*auth.Token, error) {
				return &auth.Token{}, nil
			},
			tokenSignFn: func() (string, error) {
				return "jwt-token", nil
			},
			messagingCalls: 0,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			router := mux.NewRouter()
			userRepo := &test.UserRepository{
				ByIdentityFn: tc.userFn,
			}
			repoMngr := &test.RepositoryManager{
				UserFn: func() auth.UserRepository {
					return userRepo
				},
			}
			tokenSvc := &test.TokenService{
				ValidateFn: tc.tokenValidateFn,
				CreateFn:   tc.tokenCreateFn,
				SignFn:     tc.tokenSignFn,
			}
			otpSvc := otp.NewOTP()
			messagingSvc := &test.MessagingService{}
			svc := NewService(
				WithLogger(&test.Logger{}),
				WithTokenService(tokenSvc),
				WithRepoManager(repoMngr),
				WithOTP(otpSvc),
				WithMessaging(messagingSvc),
			)

			req, err := http.NewRequest(
				"POST",
				"/api/v1/signup/verify",
				bytes.NewBuffer(tc.reqBody),
			)
			if err != nil {
				t.Fatal("failed to create request:", err)
			}

			test.SetAuthHeaders(req)

			logger := log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
			SetupHTTPHandler(svc, router, tokenSvc, logger, &httpapi.MockLimiterFactory{})

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != tc.statusCode {
				t.Errorf("incorrect status code, want %v got %v", tc.statusCode, rr.Code)
			}

			if messagingSvc.Calls.Send != tc.messagingCalls {
				t.Errorf("incorrect MessagingService.Send() call count, want %v got %v",
					tc.messagingCalls, messagingSvc.Calls.Send)
			}
		})
	}
}

func TestSignUpAPI_VerifyCodeSuccess(t *testing.T) {
	pgDB, err := test.NewPGDB()
	if err != nil {
		t.Fatal("failed to create test database:", err)
	}
	defer pgDB.DropDB()

	repoMngr := postgres.TestClient(pgDB.DB)

	ctx := context.Background()
	user := &auth.User{
		Password:  "swordfish",
		TFASecret: "tfa_secret",
		Email: sql.NullString{
			String: "jane@example.com",
			Valid:  true,
		},
		IsVerified:        false,
		IsEmailOTPAllowed: true,
	}
	err = repoMngr.User().Create(ctx, user)
	if err != nil {
		t.Fatal("failed to create uer:", err)
	}

	router := mux.NewRouter()
	tokenSvc := &test.TokenService{
		CreateFn: func() (*auth.Token, error) {
			return &auth.Token{}, nil
		},
		SignFn: func() (string, error) {
			return "jwt-token", nil
		},
		ValidateFn: func() (*auth.Token, error) {
			return &auth.Token{
				CodeHash: test.MockTokenHash("", "", time.Now().Add(time.Minute*5).Unix()),
				State:    auth.JWTPreAuthorized,
				UserID:   user.ID,
			}, nil
		},
	}
	messagingSvc := &test.MessagingService{}
	otpSvc := otp.NewOTP()

	svc := NewService(
		WithLogger(&test.Logger{}),
		WithTokenService(tokenSvc),
		WithRepoManager(repoMngr),
		WithMessaging(messagingSvc),
		WithOTP(otpSvc),
	)

	req, err := http.NewRequest(
		"POST",
		"/api/v1/signup/verify",
		bytes.NewBuffer([]byte(`{"code": "123456"}`)),
	)
	if err != nil {
		t.Fatal("failed to create request:", err)
	}

	test.SetAuthHeaders(req)

	logger := log.NewJSONLogger(log.NewSyncWriter(os.Stderr))
	SetupHTTPHandler(svc, router, tokenSvc, logger, &httpapi.MockLimiterFactory{})

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("incorrect status code, want %v got %v", http.StatusOK, rr.Code)
	}

	newUser, err := repoMngr.User().ByIdentity(ctx, "Email", user.Email.String)
	if err != nil {
		t.Fatal("failed to retrieve user:", err)
	}

	if !newUser.IsVerified {
		t.Error("user was not verified")
	}

	if messagingSvc.Calls.Send != 0 {
		t.Errorf("incorrect MessagingService.Send() call count, want 1 got %v",
			messagingSvc.Calls.Send)
	}
}
