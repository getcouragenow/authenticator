package deviceapi

import (
	"testing"
	"net/http"
	"net/http/httptest"
	"encoding/json"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/fmitra/authenticator/internal/test"
	auth "github.com/fmitra/authenticator"
)

func TestDeviceAPI_Create(t *testing.T) {
	tt := []struct{
		name string
		statusCode int
		authHeader bool
		errMessage string
		loggerCount int
		tokenValidateFn func() (*auth.Token, error)
		userFn func() (*auth.User, error)
		webauthnFn func() ([]byte, error)
	}{
		{
			name: "Authentication error with no token",
			statusCode: http.StatusUnauthorized,
			authHeader: false,
			errMessage: "user is not authenticated",
			loggerCount: 1,
			tokenValidateFn: func() (*auth.Token, error) {
				return &auth.Token{}, nil
			},
			userFn: func() (*auth.User, error) {
				return &auth.User{}, nil
			},
			webauthnFn: func() ([]byte, error) {
				return []byte(`{"result":"challenge"}`), nil
			},
		},
		{
			name: "Authentication error with bad token",
			statusCode: http.StatusUnauthorized,
			authHeader: true,
			errMessage: "bad token",
			loggerCount: 1,
			tokenValidateFn: func() (*auth.Token, error) {
				return nil, auth.ErrInvalidToken("bad token")
			},
			userFn: func() (*auth.User, error) {
				return &auth.User{}, nil
			},
			webauthnFn: func() ([]byte, error) {
				return []byte(`{"result":"challenge"}`), nil
			},
		},
		{
			name: "User query error",
			statusCode: http.StatusBadRequest,
			authHeader: true,
			errMessage: "no user found",
			loggerCount: 1,
			tokenValidateFn: func() (*auth.Token, error) {
				return &auth.Token{}, nil
			},
			userFn: func() (*auth.User, error) {
				return nil, auth.ErrBadRequest("no user found")
			},
			webauthnFn: func() ([]byte, error) {
				return []byte("challenge"), nil
			},
		},
		{
			name: "Non domain error",
			statusCode: http.StatusInternalServerError,
			authHeader: true,
			errMessage: "An internal error occurred",
			loggerCount: 1,
			tokenValidateFn: func() (*auth.Token, error) {
				return &auth.Token{}, nil
			},
			userFn: func() (*auth.User, error) {
				return nil, errors.New("whoops")
			},
			webauthnFn: func() ([]byte, error) {
				return []byte(`{"result":"challenge"}`), nil
			},
		},
		{
			name: "Successful request",
			statusCode: http.StatusOK,
			authHeader: true,
			errMessage: "",
			loggerCount: 0,
			tokenValidateFn: func() (*auth.Token, error) {
				return &auth.Token{}, nil
			},
			userFn: func() (*auth.User, error) {
				return &auth.User{}, nil
			},
			webauthnFn: func() ([]byte, error) {
				return []byte(`{"result":"challenge"}`), nil
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			router := mux.NewRouter()
			logger := &test.Logger{}
			webauthnSvc := &test.WebAuthnService{
				BeginSignUpFn: tc.webauthnFn,
			}
			repoMngr := &test.RepositoryManager{
				UserFn: func() auth.UserRepository {
					return &test.UserRepository{
						ByIdentityFn: tc.userFn,
					}
				},
			}
			tokenSvc := &test.TokenService{
				ValidateFn: tc.tokenValidateFn,
			}
			svc := NewService(
				WithLogger(&test.Logger{}),
				WithWebAuthn(webauthnSvc),
				WithRepoManager(repoMngr),
			)

			req, err := http.NewRequest("POST", "/api/v1/device", nil)
			if err != nil {
				t.Fatal("failed to create request:", err)
			}

			if tc.authHeader {
				req.Header.Set("AUTHORIZATION", "JWTTOKEN")
			}

			SetupHTTPHandler(svc, router, tokenSvc, logger)

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != tc.statusCode {
				t.Errorf("incorrect status code, want %v got %v", tc.statusCode, rr.Code)
			}

			var errResponse map[string]map[string]string
			err = json.NewDecoder(rr.Body).Decode(&errResponse)
			if err != nil && tc.errMessage != "" {
				t.Error("failed to parse response body:", err)
			}

			if logger.Calls.Log != tc.loggerCount {
				t.Errorf("incorrect calls to logger, want %v got %v",
					tc.loggerCount, logger.Calls.Log)
			}
		})
	}

}

func TestDeviceAPI_Verify(t *testing.T) {
	t.Error("whoops")
}

func TestDeviceAPI_Remove(t *testing.T) {
	t.Error("whoops")
}