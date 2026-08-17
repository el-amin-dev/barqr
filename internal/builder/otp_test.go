package builder

import (
	"strings"
	"testing"
)

func TestOTPBuild(t *testing.T) {
	t.Parallel()

	b := otpBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name: "a full totp enrolment",
			payload: map[string]any{
				"type": "totp", "issuer": "Example Ltd", "account": "ada@example.com",
				"secret": "JBSWY3DPEHPK3PXP", "algorithm": "SHA1", "digits": 6, "period": 30,
			},
			want: "otpauth://totp/Example%20Ltd:ada%40example.com?secret=JBSWY3DPEHPK3PXP" +
				"&issuer=Example%20Ltd&algorithm=SHA1&digits=6&period=30",
		},
		{
			name:    "an account alone, with totp assumed",
			payload: map[string]any{"account": "ada", "secret": "JBSWY3DPEHPK3PXP"},
			want:    "otpauth://totp/ada?secret=JBSWY3DPEHPK3PXP",
		},
		{
			name: "hotp carries a counter",
			payload: map[string]any{
				"type": "hotp", "account": "ada", "secret": "JBSWY3DPEHPK3PXP", "counter": 0,
			},
			want: "otpauth://hotp/ada?secret=JBSWY3DPEHPK3PXP&counter=0",
		},
		{
			name: "a secret is upper-cased and its display grouping removed",
			payload: map[string]any{"account": "ada", "secret": "jbsw y3dp-ehpk 3pxp"},
			want:    "otpauth://totp/ada?secret=JBSWY3DPEHPK3PXP",
		},
		{
			name:    "a secret outside the base32 alphabet is rejected",
			payload: map[string]any{"account": "ada", "secret": "not-base-32!"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing secret is rejected",
			payload: map[string]any{"account": "ada"},
			wantErr: ErrMissingField,
		},
		{
			name:    "a missing account is rejected",
			payload: map[string]any{"secret": "JBSWY3DPEHPK3PXP"},
			wantErr: ErrMissingField,
		},
		{
			name: "hotp without a counter is rejected",
			payload: map[string]any{
				"type": "hotp", "account": "ada", "secret": "JBSWY3DPEHPK3PXP",
			},
			wantErr: ErrMissingField,
		},
		{
			name: "a counter on totp is rejected",
			payload: map[string]any{
				"account": "ada", "secret": "JBSWY3DPEHPK3PXP", "counter": 1,
			},
			wantErr: ErrInvalidPayload,
		},
		{
			name: "a code length outside six to eight is rejected",
			payload: map[string]any{
				"account": "ada", "secret": "JBSWY3DPEHPK3PXP", "digits": 9,
			},
			wantErr: ErrInvalidPayload,
		},
		{
			name: "an unknown algorithm is rejected",
			payload: map[string]any{
				"account": "ada", "secret": "JBSWY3DPEHPK3PXP", "algorithm": "MD5",
			},
			wantErr: ErrInvalidPayload,
		},
		{
			name: "a colon in the issuer is rejected, being the label separator",
			payload: map[string]any{
				"issuer": "Ex:ample", "account": "ada", "secret": "JBSWY3DPEHPK3PXP",
			},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown type is rejected",
			payload: map[string]any{"type": "xotp", "account": "a", "secret": "JBSWY3DPEHPK3PXP"},
			wantErr: ErrInvalidPayload,
		},
		{
			name: "an unknown field is rejected",
			payload: map[string]any{
				"account": "ada", "secret": "JBSWY3DPEHPK3PXP", "acount": "typo",
			},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestOTPEscapesAHostileAccount(t *testing.T) {
	t.Parallel()

	// The label separator is a colon, so an account containing one has to be
	// percent-encoded or the issuer and the account would swap places.
	assertHostileRoundTrip(t, otpBuilder{}, map[string]any{
		"issuer": "Example Ltd", "account": hostileInput, "secret": "JBSWY3DPEHPK3PXP",
	}, "account")
}

// TestOTPPayloadStringRedactsTheSecret guards the credential: a stray %v must
// not put an authenticator seed in a log line.
func TestOTPPayloadStringRedactsTheSecret(t *testing.T) {
	t.Parallel()

	const secret = "JBSWY3DPEHPK3PXP"
	got := OTPPayload{Account: "ada", Secret: secret}.String()
	if strings.Contains(got, secret) {
		t.Errorf("String() leaked the secret: %q", got)
	}
	if !strings.Contains(got, redacted) {
		t.Errorf("String() = %q, want the redaction marker", got)
	}
}

func TestOTPParse(t *testing.T) {
	t.Parallel()

	b := otpBuilder{}
	assertParsed(t, b, "otpauth://totp/ada?secret=JBSWY3DPEHPK3PXP",
		map[string]any{"type": "totp", "account": "ada", "secret": "JBSWY3DPEHPK3PXP"})

	assertNotParsed(t, b,
		"otpauth://xotp/ada?secret=JBSWY3DPEHPK3PXP",   // an unknown algorithm family
		"otpauth://totp/ada?secret=not-base-32",        // a secret Build would reject
		"otpauth://totp/ada",                           // no secret
		"otpauth://totp/?secret=JBSWY3DPEHPK3PXP",      // no account
		"otpauth://hotp/ada?secret=JBSWY3DPEHPK3PXP",   // hotp with no counter
		"otpauth://totp/ada?secret=JBSWY3DPEHPK3PXP&digits=06", // not the shortest form
		"otpauth://totp/a:b?secret=JBSWY3DPEHPK3PXP&issuer=c",  // label and query disagree
		"otpauth://totp/ada?secret=JBSWY3DPEHPK3PXP#frag",      // a fragment that would be lost
	)
}
