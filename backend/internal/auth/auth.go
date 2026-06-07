package auth

import (
	"errors"
	"quanty_trade/internal/conf"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

func jwtSecretBytes() []byte {
	// 启动期 conf.MustValidateSecurity() 已经保证 JWTSecret 非空。
	// 这里仍按防御性编程检查：万一被绕过，直接 panic 让进程崩，
	// 比偷偷用默认密钥（任何人都能伪造 token）安全得多。
	if err := conf.Load(); err != nil {
		panic("jwt: conf load failed: " + err.Error())
	}
	c := conf.C()
	if c.Security.JWTSecret == "" {
		panic("jwt: JWT_SECRET 未配置，拒绝签发/校验 token。请检查启动期 conf.MustValidateSecurity() 是否被调用。")
	}
	return []byte(c.Security.JWTSecret)
}

type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func GenerateToken(userID uint, username, role string) (string, error) {
	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecretBytes())
}

func ParseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return jwtSecretBytes(), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
