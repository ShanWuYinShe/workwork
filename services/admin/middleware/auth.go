package middleware

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

func CFAuth(teamDomain, policyAUD string) func(http.Handler) http.Handler {
	issuer := strings.TrimSuffix(teamDomain, "/")
	provider, err := oidc.NewProvider(context.Background(), issuer)
	if err != nil {
		log.Fatalf("Failed to create OIDC provider for %s: %v", issuer, err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: policyAUD,
	})

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("Cf-Access-Jwt-Assertion")
			if token == "" {
				token = readCookie(r, "CF_Authorization")
			}
			if token == "" {
				http.Error(w, "Unauthorized: no CF token", http.StatusUnauthorized)
				return
			}

			idToken, err := verifier.Verify(r.Context(), token)
			if err != nil {
				log.Printf("JWT verification failed: %v", err)
				http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
				return
			}

			var claims struct {
				Email string `json:"email"`
			}
			if err := idToken.Claims(&claims); err != nil {
				log.Printf("Failed to parse token claims: %v", err)
				http.Error(w, "Unauthorized: invalid claims", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), contextKey("email"), claims.Email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func readCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

type contextKey string

func UserEmail(ctx context.Context) string {
	v, _ := ctx.Value(contextKey("email")).(string)
	return v
}
