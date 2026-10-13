package gateway

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Google sign-in verifies the ID token Google Identity Services hands the
// login page — the same flow as google-auth-library's verifyIdToken, done
// with the standard library: RS256 signature against Google's published JWKS,
// then issuer, audience (our client id), expiry, and a verified email. No
// client secret is involved; the token is minted for our client id and
// signed by Google.

const googleCertsURL = "https://www.googleapis.com/oauth2/v3/certs"

var googleIssuers = map[string]bool{"accounts.google.com": true, "https://accounts.google.com": true}

// googleClockSkew tolerates small clock drift on exp/iat.
const googleClockSkew = time.Minute

var errGoogleToken = errors.New("invalid Google ID token")

// googleClaims is the subset of ID-token claims sign-in uses.
type googleClaims struct {
	Iss           string `json:"iss"`
	Aud           string `json:"aud"`
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified any    `json:"email_verified"` // bool, or "true" in older tokens
	HD            string `json:"hd"`             // Workspace domain, absent for consumer accounts
	Name          string `json:"name"`
	Picture       string `json:"picture"`
	Exp           int64  `json:"exp"`
	Iat           int64  `json:"iat"`
}

func (c googleClaims) emailVerified() bool {
	switch v := c.EmailVerified.(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// googleVerifier caches Google's signing keys. Keys rotate; the cache lives
// as long as Google's Cache-Control says, and an unknown kid forces a
// refetch (rate-limited so garbage tokens can't hammer Google).
type googleVerifier struct {
	clientID string
	certsURL string
	client   *http.Client
	now      func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	expires   time.Time
	lastFetch time.Time
}

func newGoogleVerifier(clientID string, client *http.Client) *googleVerifier {
	return &googleVerifier{clientID: clientID, certsURL: googleCertsURL, client: client, now: time.Now}
}

var maxAgeRe = regexp.MustCompile(`max-age=(\d+)`)

// key returns the signing key for kid, refreshing the JWKS when the cache
// has expired or doesn't know the kid.
func (v *googleVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if k, ok := v.keys[kid]; ok && now.Before(v.expires) {
		return k, nil
	}
	if !v.lastFetch.IsZero() && now.Sub(v.lastFetch) < time.Minute && now.Before(v.expires) {
		return nil, fmt.Errorf("%w: unknown signing key", errGoogleToken)
	}
	keys, ttl, err := v.fetch(ctx)
	v.lastFetch = now
	if err != nil {
		return nil, fmt.Errorf("google certs: %w", err)
	}
	v.keys, v.expires = keys, now.Add(ttl)
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("%w: unknown signing key", errGoogleToken)
}

func (v *googleVerifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.certsURL, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, 0, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(keys) == 0 {
		return nil, 0, errors.New("no usable keys")
	}
	ttl := time.Hour
	if m := maxAgeRe.FindStringSubmatch(resp.Header.Get("Cache-Control")); m != nil {
		if secs, err := strconv.Atoi(m[1]); err == nil && secs > 0 {
			ttl = time.Duration(secs) * time.Second
		}
	}
	return keys, ttl, nil
}

// verify checks an ID token and returns its claims. Every rejection wraps
// errGoogleToken except a failure to reach Google's key endpoint.
func (v *googleVerifier) verify(ctx context.Context, token string) (*googleClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: malformed", errGoogleToken)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeJWTPart(parts[0], &header); err != nil || header.Alg != "RS256" || header.Kid == "" {
		return nil, fmt.Errorf("%w: bad header", errGoogleToken)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: bad signature encoding", errGoogleToken)
	}
	pub, err := v.key(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return nil, fmt.Errorf("%w: signature", errGoogleToken)
	}
	var c googleClaims
	if err := decodeJWTPart(parts[1], &c); err != nil {
		return nil, fmt.Errorf("%w: bad claims", errGoogleToken)
	}
	now := v.now()
	switch {
	case !googleIssuers[c.Iss]:
		return nil, fmt.Errorf("%w: issuer", errGoogleToken)
	case c.Aud != v.clientID:
		return nil, fmt.Errorf("%w: audience", errGoogleToken)
	case now.After(time.Unix(c.Exp, 0).Add(googleClockSkew)):
		return nil, fmt.Errorf("%w: expired", errGoogleToken)
	case c.Iat != 0 && time.Unix(c.Iat, 0).After(now.Add(googleClockSkew)):
		return nil, fmt.Errorf("%w: issued in the future", errGoogleToken)
	case c.Email == "" || !c.emailVerified():
		return nil, fmt.Errorf("%w: email not verified", errGoogleToken)
	}
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	return &c, nil
}

func decodeJWTPart(seg string, dst any) error {
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// googleAvatarURL returns the token's profile picture when it is an https
// URL on Google's avatar host, else "". The token is Google-signed, but the
// GUI renders this URL in an <img>, so only the expected host is kept.
func googleAvatarURL(c *googleClaims) string {
	u, err := url.Parse(c.Picture)
	if err != nil || u.Scheme != "https" || u.User != nil || len(c.Picture) > 2048 {
		return ""
	}
	if h := strings.ToLower(u.Hostname()); h != "googleusercontent.com" && !strings.HasSuffix(h, ".googleusercontent.com") {
		return ""
	}
	return c.Picture
}

// googleDomainAllowed reports whether the token's account belongs to the
// configured Workspace domain. Both the hd claim (set only for Workspace
// accounts) and the email's domain must match — a consumer account whose
// address happens to end in the domain carries no hd and is refused.
func googleDomainAllowed(c *googleClaims, domain string) bool {
	if domain == "" {
		return true
	}
	at := strings.LastIndexByte(c.Email, '@')
	return at > 0 && strings.EqualFold(c.Email[at+1:], domain) && strings.EqualFold(c.HD, domain)
}
