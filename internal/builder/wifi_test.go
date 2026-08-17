package builder

import (
	"strings"
	"testing"
)

func TestWiFiBuild(t *testing.T) {
	t.Parallel()

	b := wifiBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name:    "a WPA network",
			payload: map[string]any{"ssid": "Cafe Guest", "password": "correct horse", "auth": "WPA"},
			want:    "WIFI:T:WPA;S:Cafe Guest;P:correct horse;;",
		},
		{
			name:    "an open network omits the passphrase field",
			payload: map[string]any{"ssid": "Airport Free"},
			want:    "WIFI:T:nopass;S:Airport Free;;",
		},
		{
			name:    "a passphrase implies WPA",
			payload: map[string]any{"ssid": "Home", "password": "hunter2hunter2"},
			want:    "WIFI:T:WPA;S:Home;P:hunter2hunter2;;",
		},
		{
			name:    "a hidden network carries H",
			payload: map[string]any{"ssid": "Hidden", "password": "hunter2hunter2", "hidden": true},
			want:    "WIFI:T:WPA;S:Hidden;P:hunter2hunter2;H:true;;",
		},
		{
			name:    "an all-hex ssid is quoted so it is not read as a raw key",
			payload: map[string]any{"ssid": "abcdef", "auth": "nopass"},
			want:    `WIFI:T:nopass;S:"abcdef";;`,
		},
		{
			name:    "WPA2 is spelled WPA in this format",
			payload: map[string]any{"ssid": "Home", "password": "hunter2hunter2", "auth": "wpa2"},
			want:    "WIFI:T:WPA;S:Home;P:hunter2hunter2;;",
		},
		{
			name:    "a thirteen character WEP key",
			payload: map[string]any{"ssid": "Old", "password": "abcdefghijklm", "auth": "WEP"},
			want:    "WIFI:T:WEP;S:Old;P:abcdefghijklm;;",
		},
		{
			name:    "a ten digit hex WEP key is quoted, being all hex",
			payload: map[string]any{"ssid": "Old", "password": "0123456789", "auth": "WEP"},
			want:    `WIFI:T:WEP;S:Old;P:"0123456789";;`,
		},
		{
			name:    "an enterprise credential may be shorter than a WPA passphrase",
			payload: map[string]any{"ssid": "Corp", "password": "pw", "auth": "WPA2-EAP"},
			want:    "WIFI:T:WPA2-EAP;S:Corp;P:pw;;",
		},
		{
			name:    "an enterprise credential with no password is rejected",
			payload: map[string]any{"ssid": "Corp", "auth": "WPA2-EAP"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a WEP key of the wrong length is rejected",
			payload: map[string]any{"ssid": "Old", "password": "abcdefg", "auth": "WEP"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a short WPA passphrase is rejected",
			payload: map[string]any{"ssid": "Home", "password": "short", "auth": "WPA"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a passphrase on an open network is rejected",
			payload: map[string]any{"ssid": "Home", "password": "hunter2hunter2", "auth": "nopass"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown auth type is rejected",
			payload: map[string]any{"ssid": "Home", "password": "hunter2hunter2", "auth": "WPA4"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an over-long ssid is rejected",
			payload: map[string]any{"ssid": strings.Repeat("s", 33)},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing ssid is rejected",
			payload: map[string]any{"password": "hunter2hunter2"},
			wantErr: ErrMissingField,
		},
		{
			name:    "a non-boolean hidden is rejected",
			payload: map[string]any{"ssid": "Home", "hidden": "ture"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"ssid": "Home", "ssdi": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestWiFiEscapesHostileCredentials(t *testing.T) {
	t.Parallel()

	b := wifiBuilder{}
	assertHostileRoundTrip(t, b, map[string]any{
		"ssid": hostileInput, "auth": "nopass",
	}, "ssid")
	assertHostileRoundTrip(t, b, map[string]any{
		"ssid": "Home", "password": hostileInput, "auth": "WPA",
	}, "password")

	raw, err := b.Build(map[string]any{"ssid": hostileInput, "auth": "nopass"})
	if err != nil {
		t.Fatalf("Build = error %v", err)
	}
	// The record ends at ";;", so an unescaped semicolon inside the ssid would
	// truncate it. There must be exactly one such terminator, at the end.
	if strings.Index(raw, ";;") != len(raw)-2 {
		t.Errorf("an unescaped delimiter leaked into %q", raw)
	}
}

func TestWiFiBuildAcceptsItsStruct(t *testing.T) {
	t.Parallel()

	got, err := wifiBuilder{}.Build(WiFiPayload{
		SSID: "Home", Password: "hunter2hunter2", Auth: "WPA", Hidden: true,
	})
	if err != nil {
		t.Fatalf("Build(WiFiPayload) = error %v", err)
	}
	if want := "WIFI:T:WPA;S:Home;P:hunter2hunter2;H:true;;"; got != want {
		t.Errorf("Build(WiFiPayload) = %q, want %q", got, want)
	}
}

// TestWiFiPayloadStringRedactsThePassword guards the secret: a stray %v on the
// payload must not put a network key in a log line.
func TestWiFiPayloadStringRedactsThePassword(t *testing.T) {
	t.Parallel()

	const secret = "hunter2hunter2"
	got := WiFiPayload{SSID: "Home", Password: secret}.String()
	if strings.Contains(got, secret) {
		t.Errorf("String() leaked the passphrase: %q", got)
	}
	if !strings.Contains(got, redacted) {
		t.Errorf("String() = %q, want the redaction marker", got)
	}
}

func TestWiFiParse(t *testing.T) {
	t.Parallel()

	b := wifiBuilder{}
	// Records in the wild leave T out for an open network; the default is
	// filled in rather than left absent.
	assertParsed(t, b, "WIFI:S:Airport Free;;",
		map[string]any{"ssid": "Airport Free", "auth": authNone})
	assertParsed(t, b, "WIFI:T:WPA;S:Home;P:hunter2hunter2;H:false;;",
		map[string]any{"ssid": "Home", "password": "hunter2hunter2",
			"auth": authWPA, "hidden": false})

	assertNotParsed(t, b,
		"WIFI:T:WPA;S:Home;P:hunter2hunter2;",         // no record terminator
		"WIFI:T:WPA;S:Home;P:short;;",                 // a passphrase Build would reject
		"WIFI:T:WPA;S:Home;P:hunter2hunter2;E:PEAP;;", // an enterprise field not modelled
		"WIFI:T:WPA;P:hunter2hunter2;;",               // no ssid
		"WIFI:;;",
	)
}
