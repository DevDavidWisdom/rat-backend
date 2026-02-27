package services

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/config"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserInactive       = errors.New("user account is inactive")
	ErrTokenExpired       = errors.New("token has expired")
	ErrInvalidToken       = errors.New("invalid token")
)

type AuthService struct {
	userRepo *repository.UserRepository
	config   *config.Config
}

type Claims struct {
	UserID         uuid.UUID       `json:"user_id"`
	OrganizationID uuid.UUID       `json:"org_id"`
	Email          string          `json:"email"`
	Role           models.UserRole `json:"role"`
	TokenType      string          `json:"type"` // "access" or "refresh"
	jwt.RegisteredClaims
}

type DeviceClaims struct {
	DeviceID       uuid.UUID `json:"device_id"`
	DeviceIdentity string    `json:"device_identity"`
	OrganizationID uuid.UUID `json:"org_id"`
	jwt.RegisteredClaims
}

func NewAuthService(userRepo *repository.UserRepository, cfg *config.Config) *AuthService {
	return &AuthService{
		userRepo: userRepo,
		config:   cfg,
	}
}

func (s *AuthService) Register(ctx context.Context, req *models.RegisterRequest, orgID uuid.UUID) (*models.User, error) {
	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &models.User{
		OrganizationID: orgID,
		Email:          req.Email,
		PasswordHash:   string(hashedPassword),
		Name:           req.Name,
		Role:           models.UserRoleAdmin,
		IsActive:       true,
	}

	err = s.userRepo.Create(ctx, user)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func (s *AuthService) Login(ctx context.Context, req *models.LoginRequest) (*models.LoginResponse, error) {
	log.Printf("[AUTH] Login attempt: email=%s", req.Email)
	log.Printf("[AUTH] Expected admin email=%s, password_match=%v", s.config.Admin.Email, req.Password == s.config.Admin.Password)

	// Check for hardcoded system admin from environment
	if req.Email == s.config.Admin.Email && req.Password == s.config.Admin.Password {
		log.Printf("[AUTH] System admin login matched")
		orgID, _ := uuid.Parse(s.config.Admin.OrganizationID)
		user := &models.User{
			ID:             uuid.MustParse("00000000-0000-0000-0000-000000000000"), // Fixed UUID for system admin
			OrganizationID: orgID,
			Email:          req.Email,
			Name:           "System Administrator",
			Role:           models.UserRoleSuperAdmin,
			IsActive:       true,
		}

		accessToken, err := s.generateAccessToken(user)
		if err != nil {
			return nil, err
		}

		refreshToken, err := s.generateRefreshToken(user)
		if err != nil {
			return nil, err
		}

		expiresAt := time.Now().Add(time.Duration(s.config.JWT.ExpiryHours) * time.Hour).Unix()

		return &models.LoginResponse{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    expiresAt,
			User:         *user,
		}, nil
	}

	user, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			log.Printf("[AUTH] User not found in DB: %s", req.Email)
			return nil, ErrInvalidCredentials
		}
		log.Printf("[AUTH] DB error looking up user: %v", err)
		return nil, err
	}

	// Check password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		log.Printf("[AUTH] Password mismatch for DB user: %s", req.Email)
		return nil, ErrInvalidCredentials
	}

	// Check if user is active
	if !user.IsActive {
		return nil, ErrUserInactive
	}

	// Generate tokens
	accessToken, err := s.generateAccessToken(user)
	if err != nil {
		return nil, err
	}

	refreshToken, err := s.generateRefreshToken(user)
	if err != nil {
		return nil, err
	}

	// Update last login
	_ = s.userRepo.UpdateLastLogin(ctx, user.ID)

	expiresAt := time.Now().Add(time.Duration(s.config.JWT.ExpiryHours) * time.Hour).Unix()

	return &models.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		User:         *user,
	}, nil
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*models.LoginResponse, error) {
	claims, err := s.ValidateToken(refreshToken)
	if err != nil {
		return nil, err
	}

	if claims.TokenType != "refresh" {
		return nil, ErrInvalidToken
	}

	var user *models.User

	// Check if this is the hardcoded system admin
	if claims.UserID == uuid.MustParse("00000000-0000-0000-0000-000000000000") {
		orgID, _ := uuid.Parse(s.config.Admin.OrganizationID)
		user = &models.User{
			ID:             claims.UserID,
			OrganizationID: orgID,
			Email:          s.config.Admin.Email,
			Name:           "System Administrator",
			Role:           models.UserRoleSuperAdmin,
			IsActive:       true,
		}
	} else {
		user, err = s.userRepo.GetByID(ctx, claims.UserID)
		if err != nil {
			return nil, err
		}
	}

	if !user.IsActive {
		return nil, ErrUserInactive
	}

	accessToken, err := s.generateAccessToken(user)
	if err != nil {
		return nil, err
	}

	newRefreshToken, err := s.generateRefreshToken(user)
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(time.Duration(s.config.JWT.ExpiryHours) * time.Hour).Unix()

	return &models.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresAt:    expiresAt,
		User:         *user,
	}, nil
}

func (s *AuthService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(s.config.JWT.Secret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}

func (s *AuthService) generateAccessToken(user *models.User) (string, error) {
	expiresAt := time.Now().Add(time.Duration(s.config.JWT.ExpiryHours) * time.Hour)

	claims := &Claims{
		UserID:         user.ID,
		OrganizationID: user.OrganizationID,
		Email:          user.Email,
		Role:           user.Role,
		TokenType:      "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   user.ID.String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.config.JWT.Secret))
}

func (s *AuthService) generateRefreshToken(user *models.User) (string, error) {
	expiresAt := time.Now().Add(time.Duration(s.config.JWT.RefreshDays) * 24 * time.Hour)

	claims := &Claims{
		UserID:         user.ID,
		OrganizationID: user.OrganizationID,
		Email:          user.Email,
		Role:           user.Role,
		TokenType:      "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   user.ID.String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.config.JWT.Secret))
}

func (s *AuthService) GenerateDeviceToken(device *models.Device) (string, error) {
	expiresAt := time.Now().Add(time.Duration(s.config.JWT.DeviceExpiryDay) * 24 * time.Hour)

	claims := &DeviceClaims{
		DeviceID:       device.ID,
		DeviceIdentity: device.DeviceID,
		OrganizationID: device.OrganizationID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   device.ID.String(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.config.JWT.Secret))
}

func (s *AuthService) ValidateDeviceToken(tokenString string) (*DeviceClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &DeviceClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(s.config.JWT.Secret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*DeviceClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}
