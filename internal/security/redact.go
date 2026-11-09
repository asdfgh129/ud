package security

import (
	"net/url"
	"regexp"
	"strings"
)

// sensitive keys are taken from docs/11 §3 / docs/12 §6 credential scan.
var sensitiveKeys = map[string]bool{
	"token": true, "access_token": true, "refreshtoken": true, "passkey": true,
	"password": true, "passwd": true, "pwd": true, "secret": true,
	"session": true, "sessionid": true, "sid": true, "sig": true, "signature": true,
	"auth": true, "authorization": true, "apikey": true, "api_key": true,
	"key": true, "cookie": true, "credential": true, "otp": true,
	"setcookie": true, "proxyauthorization": true, "xapikey": true,
}

func isSensitiveKey(k string) bool {
	norm := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(k))
	return sensitiveKeys[norm]
}

// IsSensitiveHeader reports whether a header must never be persisted in
// plaintext (docs/06 §6: DB stores only cred_ref for Cookie / Authorization).
func IsSensitiveHeader(k string) bool { return isSensitiveKey(k) }

// StripSensitiveHeaders drops credential-bearing headers instead of masking
// them. A masked "***" value is harmless in a log but wrong in storage: on
// resume the Scheduler would replay it as a real header. Callers that still
// need the secret must go through CredentialStore + credentialRef.
// It returns nil for an empty result so `omitempty` keeps payloads compact.
func StripSensitiveHeaders(h map[string]string) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if !isSensitiveKey(k) {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var headerValuePattern = regexp.MustCompile(
	`(?i)(authorization|cookie|set-cookie|proxy-authorization|x-api-key|passkey)\s*[:=]\s*(?:bearer\s+|basic\s+)?[^\s,;"']+`)

var queryValuePattern = regexp.MustCompile(
	`(?i)([?&])([^=&\s]*(?:token|passkey|password|secret|session|sig|key|auth)[^=&\s]*)=([^&\s]*)`)

// RedactURL removes credentials from URL userinfo and sensitive query values.
// It is applied to tasks.input before persistence (docs/06 §6).
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return queryValuePattern.ReplaceAllString(raw, "${1}${2}=***")
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			if isSensitiveKey(k) {
				q.Set(k, "***")
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// Redact scrubs free-form log strings and error details.
func Redact(s string) string {
	s = headerValuePattern.ReplaceAllString(s, "$1: ***")
	s = queryValuePattern.ReplaceAllString(s, "${1}${2}=***")
	return s
}

// RedactHeaders returns a copy with sensitive values replaced.
func RedactHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if isSensitiveKey(k) {
			out[k] = "***"
		} else {
			out[k] = Redact(v)
		}
	}
	return out
}

// ContainsSecret is used by tests and the CI credential-leak scan helper.
func ContainsSecret(s string) bool {
	lower := strings.ToLower(s)
	for _, needle := range []string{"cookie:", "authorization:", "passkey=", "session="} {
		if strings.Contains(lower, needle) && !strings.Contains(lower, "***") {
			return true
		}
	}
	return false
}
