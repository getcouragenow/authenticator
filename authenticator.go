// Package authenticator defines the domain model for user authentication.
package authenticator

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/dgrijalva/jwt-go"
)

// TokenState represents a state of a JWT token.
// A token may represent an intermediary state prior
// to authorization (ex. TOTP code is required)
type TokenState string

// DeliveryMethod represents a mechanism to send messages
// to users.
type DeliveryMethod string

// TFAOptions represents options a user may use to complete
// 2FA.
type TFAOptions string

const (
	// OTPEmail allows a user to complete TFA with an OTP
	// code delivered via email.
	OTPEmail TFAOptions = "otp_email"
	// OTPPhone allows a user to complete TFA with an OTP
	// code delivered via phone.
	OTPPhone = "otp_phone"
	// TOTP allows a user to complete TFA with a TOTP
	// device or application.
	TOTP = "totp"
	// Webauthn allows a user to complete TFA with a Webauthn
	// device.
	Webauthn = "webauthn"
)

const (
	// Phone is a delivery method for text messages.
	Phone DeliveryMethod = "phone"
	// Email is a delivery method for email.
	Email = "email"
)

const (
	// Issuer is the default issuer of a JWT token.
	Issuer = "authenticator"
)

const (
	// NoPassword specifies User registration and authentication
	// is allowed without a password. This allows user onboarding
	// through verification of an email or SMS token.
	NoPassword = "no_password"
	// Password specifies User registration and authentication
	// must be completed with a password at all times.
	Password = "password"
)

const (
	// IDPhone specifies we allow registration
	// with a phone number.
	IDPhone = "phone"
	// IDEmail specifies we allow registration with
	// an email address.
	IDEmail = "email"
	// IDContact specifies we allow registration with
	// either a phone number or email.
	IDContact = "contact"
)

const (
	// JWTPreAuthorized represents the state of a user before completing
	// the TFA step of signup or login.
	JWTPreAuthorized TokenState = "pre_authorized"
	// JWTAuthorized represents a the state of a user after completing
	// the final step of login or signup.
	JWTAuthorized TokenState = "authorized"
)

// User represents a user who is registered with the service.
type User struct {
	// ID is a unique ID for the user.
	ID string
	// Phone is a phone number associated with the account.
	Phone sql.NullString
	// Email is an email address associated with the account.
	Email sql.NullString
	// Password is the current User provided password.
	Password string
	// TFASecret is a a secret string used to generate 2FA TOTP codes.
	TFASecret string
	// IsPhoneAllowed specifies a user may complete authentication
	// by verifying an OTP code delivered through SMS.
	IsPhoneOTPAllowed bool
	// IsEmailOTPAllowed specifies a user may complete authentication
	// by verifying an OTP code delivered through email.
	IsEmailOTPAllowed bool
	// IsTOTPAllowed specifies a user may complete authentication
	// by verifying a TOTP code.
	IsTOTPAllowed bool
	// IsDeviceAllowed specifies a user may complete authentication
	// by verifying a WebAuthn capable device.
	IsDeviceAllowed bool
	// IsVerified tells us if a user confirmed ownership of
	// an email or phone number by validating a one time code
	// after registration.
	IsVerified bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// DefaultOTPDelivery returns the default OTP delivery method.
func (u *User) DefaultOTPDelivery() DeliveryMethod {
	if u.Email.String != "" {
		return Email
	}

	return Phone
}

// CanSendDefaultOTP determines if an OTP code should be sent out
// to a user immediately as a 2FA option.
func (u *User) CanSendDefaultOTP() bool {
	if u.IsDeviceAllowed || u.IsTOTPAllowed {
		return false
	}

	isOTPEnabled := u.IsPhoneOTPAllowed || u.IsEmailOTPAllowed
	return isOTPEnabled
}

// DefaultName returns the default name for a user (email or phone).
func (u *User) DefaultName() string {
	if u.Email.String != "" {
		return u.Email.String
	}
	return u.Phone.String
}

// Device represents a device capable of attesting to a User's
// identity. Examples options include a FIDO U2F key or
// fingerprint sensor.
type Device struct {
	// ID is a unique service ID for the device.
	ID string
	// UserID is the User's ID associated with the device
	UserID string
	// ClientID is a non unique ID generated by the client
	// during device registration.
	ClientID []byte
	// PublicKey is the public key of a device used for signing
	// purposes.
	PublicKey []byte
	// Name is a User supplied human readable name for a device.
	Name string
	// AAGUID is the globally unique identifier of the authentication
	// device.
	AAGUID []byte
	// SignCount is the stored signature counter of the device.
	// This value is increment to match the device counter on
	// each successive authentication. If the our value is larger
	// than or equal to the device value, it is indicative that the
	// device may be cloned or malfunctioning.
	SignCount uint32
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LoginHistory represents a login associated with a user.
type LoginHistory struct {
	// TokenID is the ID of a JWT token.
	TokenID string
	// UserID is the User's ID associated with the login record.
	UserID string
	// IsRevoked is a boolean indicating the token has
	// been revoked. Tokens are invalidated through
	// expiry or revocation.
	IsRevoked bool
	// ExpiresAt is the expiry time of the JWT token.
	ExpiresAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Token is a token that provides proof of User authentication.
type Token struct {
	// jwt.StandardClaims provides standard JWT fields
	// such as Audience, ExpiresAt, Id, Issuer.
	jwt.StandardClaims
	// ClientID is the unhashed ID stored securely on the client and used
	// to validate the token request source. It is not embedded in the
	// the JWT token body.
	ClientID string `json:"-"`
	// ClientIDHash is hash of an ID stored in the client for which
	// the token was delivered too. A token is only valid when the
	// hash's corresponding ClientID is delivered alongside the JWT token.
	ClientIDHash string `json:"client_id"`
	// UserID is the User's ID.
	UserID string `json:"user_id"`
	// Email is a User's email.
	Email string `json:"email"`
	// Phone is a User's phone number.
	Phone string `json:"phone_number"`
	// State is the current state of the user at the time
	// the token was issued.
	State TokenState `json:"state"`
	// CodeHash is the hash of a randomly generated code used
	// to validate an OTP code and escalate the token to an
	// authorized token.
	CodeHash string `json:"code,omitempty"`
	// Code is the unhashed value of CodeHash. This value is
	// not persisted and returned to the client outside of the JWT
	// response through an alternative mechanism (e.g. Email). It is
	// validated by ensuring the SHA512 hash of the value matches the
	// CodeHash embedded in the token.
	Code string `json:"-"`
	// TFAOptions represents available options a user may use to complete
	// 2FA.
	TFAOptions []TFAOptions `json:"tfa_options"`
}

// Message is a message to be delivered to a user.
type Message struct {
	// Delivery type of the message (e.g. phone or email).
	Delivery DeliveryMethod
	// Content of the message.
	Content string
	// Delivery address of the user (e.g. phone or email).
	Address string
	// ExpiresAt is the latest time we can attempt delivery.
	ExpiresAt time.Time
	// DeliveryAttempts is the total amount of delivery attempts made.
	DeliveryAttempts int
}

// MessageRepository represents a local storage for outgoing messages.
// This service will deliver OTP codes via email or SMS if enabled for the user.
type MessageRepository interface {
	// Publish prepares a message for a user. Behind the scenes we write the
	// message into a channel to be processed by a consumer.
	Publish(ctx context.Context, msg *Message) error
	// Recent retrieves a list of messages to be delivered.
	Recent(ctx context.Context) (<-chan *Message, <-chan error)
}

// LoginHistoryRepository represents a local storage for LoginHistory.
type LoginHistoryRepository interface {
	// ByUserID retrieves recent LoginHistory associated with a User's ID.
	// It supports pagination through a limit or offset value.
	ByUserID(ctx context.Context, userID string, limit, offset int) ([]*LoginHistory, error)
	// Create creates a new LoginHistory.
	Create(ctx context.Context, login *LoginHistory) error
	// GetForUpdate retrieves a LoginHistory by TokenID for updating.
	GetForUpdate(ctx context.Context, tokenID string) (*LoginHistory, error)
	// Update updates a LoginHistory.
	Update(ctx context.Context, login *LoginHistory) error
}

// DeviceRepository represents a local storage for Device.
type DeviceRepository interface {
	// ByID returns a Device by it's ID.
	ByID(ctx context.Context, deviceID string) (*Device, error)
	// ByClientID retrieves a Device associated with a User
	// by a ClientID.
	ByClientID(ctx context.Context, userID string, clientID []byte) (*Device, error)
	// ByUserID retreives all Devices associated with a User.
	ByUserID(ctx context.Context, userID string) ([]*Device, error)
	// Create creates a new Device record.
	Create(ctx context.Context, device *Device) error
	// GetForUpdate retrieves a Device by ID for updating.
	GetForUpdate(ctx context.Context, deviceID string) (*Device, error)
	// Update updates a Device.
	Update(ctx context.Context, device *Device) error
	// Removes a Devie associated with a User.
	Remove(ctx context.Context, deviceID, userID string) error
}

// UserRepository represents a local storage for User.
type UserRepository interface {
	// ByIdentity retrieves a User by some whitelisted identity
	// value such as email, phone, username, ID.
	ByIdentity(ctx context.Context, attribute, value string) (*User, error)
	// GetForUpdate retrieves a User by ID for updating.
	GetForUpdate(ctx context.Context, userID string) (*User, error)
	// Create creates a new User Record.
	Create(ctx context.Context, u *User) error
	// ReCreate updates an existing, unverified User record,
	// to treat the entry as a new unverified User registration.
	// User's are considered unverified until completing OTP
	// verification to prove ownership of a phone or email address.
	ReCreate(ctx context.Context, u *User) error
	// Update updates a User.
	Update(ctx context.Context, u *User) error
	// DisableOTP disables an OTP method for a User.
	DisableOTP(ctx context.Context, userID string, method DeliveryMethod) (*User, error)
	// RemoveDeliveryMethod removes a phone or email from a User.
	RemoveDeliveryMethod(ctx context.Context, userID string, method DeliveryMethod) (*User, error)
}

// RepositoryManager manages repositories stored in storages
// with atomic properties.
type RepositoryManager interface {
	// NewWithTransaction returns a new manager to with a transaction
	// enabled.
	NewWithTransaction(ctx context.Context) (RepositoryManager, error)
	// WithAtomic performs an operation inside of a transaction.
	// On success, it will return an entity.
	WithAtomic(operation func() (interface{}, error)) (interface{}, error)
	// LoginHistory returns a LoginHistoryRepository.
	LoginHistory() LoginHistoryRepository
	// Device returns a DeviceRepository.
	Device() DeviceRepository
	// User returns a UserRepository.
	User() UserRepository
}

// TokenService represents a service to manage JWT tokens.
type TokenService interface {
	// Create creates a new authorized or pre-authorized JWT token.
	// On success, it returns the token.
	Create(ctx context.Context, user *User, state TokenState) (*Token, error)
	// CreateWithOTP creates a new authorized or pre-authorized JWT token
	// with embedded OTP code for 2FA verification.
	CreateWithOTP(ctx context.Context, user *User, state TokenState, method DeliveryMethod) (*Token, error)
	CreateWithOTPAndAddress(ctx context.Context, user *User, state TokenState, method DeliveryMethod, addr string) (*Token, error)
	// Sign creates a signed JWT token string from a token struct.
	Sign(ctx context.Context, token *Token) (string, error)
	// Validate checks that a JWT token is signed by us, unexpired,
	// unrevoked, and from the a valid client. On success it will return the unpacked
	// Token struct.
	Validate(ctx context.Context, signedToken string, clientID string) (*Token, error)
	// Revoke Revokes a token for a specified duration of time.
	Revoke(ctx context.Context, tokenID string, duration time.Duration) error
	// Cookie returns a secure cookie to accompany a token.
	Cookie(ctx context.Context, token *Token) *http.Cookie
	// Refresh refreshes an expiring token.
	Refresh(ctx context.Context, token *Token, refreshKey string) (*Token, error)
}

// WebAuthnService manages the protocol for WebAuthn authentication.
type WebAuthnService interface {
	// BeginSignUp attempts to register a new WebAuthn device.
	BeginSignUp(ctx context.Context, user *User) ([]byte, error)
	// FinishSignUp confirms a challenge signature for registration.
	FinishSignUp(ctx context.Context, user *User, r *http.Request) (*Device, error)
	// BeginLogin starts the authentication flow to validate a device.
	BeginLogin(ctx context.Context, user *User) ([]byte, error)
	// FinishLogin confirms that a device successfully signed a challenge.
	FinishLogin(ctx context.Context, user *User, r *http.Request) error
}

// PasswordService manages the protocol for password management and validation.
type PasswordService interface {
	// Hash hashes a password for storage.
	Hash(password string) ([]byte, error)
	// Validate determines if a submitted pasword is valid for a stored
	// password hash.
	Validate(user *User, password string) error
	// OKForUser checks if a password may be used for a user.
	OKForUser(password string) error
}

// OTPService manages the protocol for SMS/Email 2FA codes and TOTP codes.
type OTPService interface {
	// TOTPQRString returns a URL string used for TOTP code generation.
	TOTPQRString(u *User) (string, error)
	// TOTPSecret creates a TOTP secret for code generation.
	TOTPSecret(u *User) (string, error)
	// OTPCode creates a random OTP code and hash.
	OTPCode(address string, method DeliveryMethod) (code, hash string, err error)
	// ValidateOTP checks if a User email/sms delivered OTP code is valid.
	ValidateOTP(code, hash string) error
	// ValidateTOTP checks if a User TOTP code is valid.
	ValidateTOTP(user *User, code string) error
}

// MessagingService sends messages through email or SMS.
type MessagingService interface {
	// Send sends a message to a user.
	Send(ctx context.Context, message, addr string, method DeliveryMethod) error
}

// LoginAPI provides HTTP handlers for user authentication.
type LoginAPI interface {
	// Login is the initial login step to identify a User.
	// On success it will return a JWT token in an identified state.
	Login(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// DeviceChallenge retrieves a device challenge to be signed by the client.
	DeviceChallenge(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// VerifyDevice verifies a User's authenticity by verifying
	// a signing device owned by the user. On success it will return
	// a JWT token in an authorized state.
	VerifyDevice(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// VerifyCode verifies a User's authenticity by verifying
	// a TOTP or randomly generated code delivered by SMS/Email.
	// On success it will return a JWT token in an auhtorized state.
	VerifyCode(w http.ResponseWriter, r *http.Request) (interface{}, error)
}

// SignUpAPI provides HTTP handlers for user registration.
type SignUpAPI interface {
	// SignUp is the initial registration step to identify a User.
	// On success it will return a JWT token in an unverified state.
	SignUp(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Verify is the final registration step to validate a new
	// User's authenticity. On success it will return a JWT
	// token in an authozied state.
	Verify(w http.ResponseWriter, r *http.Request) (interface{}, error)
}

// ContactAPI provides HTTP handlers to manage email/SMS configuration for a User.
type ContactAPI interface {
	// CheckAddress requests an OTP code to be delivered to a user through
	// an email address or phone number.
	CheckAddress(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Disable disables a verified email or phone number on a user's profile
	// from receiving OTP codes.
	Disable(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Verify verifies an OTP code sent to an email or phone number. If
	// delivery address is new to the user, we set it on the profile.
	// By default, verified addresses are enabled for future OTP
	// code delivery unless the client explicitly says otherwise.
	Verify(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Remove removes a verified email or phone number from the User's profile.
	// Removed email addresses and phone numbers cannot be re-added without
	// requesting a new OTP.
	Remove(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Send allows a user to request an OTP code to be delivered to them through
	// a pre-approved channel.
	Send(w http.ResponseWriter, r *http.Request) (interface{}, error)
}

// TOTPAPI provides HTTP handlers to manage TOTP configuration for a User.
type TOTPAPI interface {
	// Secret requests a TOTP secret to allow a user to generate TOTP codes
	// via a supported application (e.g. Google Authenticator).
	Secret(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Verify enables TOTP as an TFA option for a user by accepting a
	// TOTP code and validating it against the TFA secret configured
	// on the user.
	Verify(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Remove disables TOTP as an TFA option for a user by accepting a
	// TOTP code and validating it against the TFA secret configured
	// on the user.
	Remove(w http.ResponseWriter, r *http.Request) (interface{}, error)
}

// DeviceAPI provides HTTP handlers to manage Devices for a User.
type DeviceAPI interface {
	// Verify validates ownership of a new Device for a User.
	Verify(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Create is an initial request to add a new Device for a User.
	Create(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Remove removes a Device associated with a User.
	Remove(w http.ResponseWriter, r *http.Request) (interface{}, error)
}

// TokenAPI provides HTTP handlers to manage a User's tokens.
type TokenAPI interface {
	// Revoke revokes a User's token for a logged in session.
	Revoke(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Verify verifies a User's token is authenticated and
	// valid. A valid token is not expired and not revoked.
	Verify(w http.ResponseWriter, r *http.Request) (interface{}, error)
	// Refresh refreshes a non-expired token with a new expiriry time.
	// Refreshed tokens always have default permissions.
	Refresh(w http.ResponseWriter, r *http.Request) (interface{}, error)
}

// UserAPI proivdes HTTP handlers to configure a registered User's
// account.
type UserAPI interface {
	// UpdatePassword change's a User's password.
	UpdatePassword(w http.ResponseWriter, r *http.Request) (interface{}, error)
}
