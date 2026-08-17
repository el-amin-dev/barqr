package builder

import (
	"strings"
	"testing"
)

// A published example IBAN with valid check digits.
const testIBAN = "DE89370400440532013000"

func TestEPCBuild(t *testing.T) {
	t.Parallel()

	b := epcBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name: "a full transfer",
			payload: map[string]any{
				"bic": "BANKGB2L", "name": "Example Ltd", "iban": testIBAN,
				"amount": 12.5, "purpose": "GDDS", "reference": "RF18539007547034",
				"information": "Thank you",
			},
			want: "BCD\n002\n1\nSCT\nBANKGB2L\nExample Ltd\n" + testIBAN +
				"\nEUR12.50\nGDDS\nRF18539007547034\n\nThank you",
		},
		{
			name:    "trailing empty fields are dropped",
			payload: map[string]any{"name": "Example Ltd", "iban": testIBAN},
			want:    "BCD\n002\n1\nSCT\n\nExample Ltd\n" + testIBAN,
		},
		{
			name: "unstructured remittance instead of a reference",
			payload: map[string]any{
				"name": "Example Ltd", "iban": testIBAN, "remittance": "Invoice 2026-114",
			},
			want: "BCD\n002\n1\nSCT\n\nExample Ltd\n" + testIBAN + "\n\n\n\nInvoice 2026-114",
		},
		{
			name:    "an iban is accepted in the groups a statement prints",
			payload: map[string]any{"name": "Example Ltd", "iban": "de89 3704 0044 0532 0130 00"},
			want:    "BCD\n002\n1\nSCT\n\nExample Ltd\n" + testIBAN,
		},
		{
			name:    "a mistyped iban fails its check digits",
			payload: map[string]any{"name": "Example Ltd", "iban": "DE89370400440532013001"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an iban of the wrong shape is rejected",
			payload: map[string]any{"name": "Example Ltd", "iban": "NOT-AN-IBAN"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a malformed bic is rejected",
			payload: map[string]any{"name": "Example Ltd", "iban": testIBAN, "bic": "BANK"},
			wantErr: ErrInvalidPayload,
		},
		{
			name: "reference and remittance together are rejected",
			payload: map[string]any{
				"name": "Example Ltd", "iban": testIBAN,
				"reference": "RF18539007547034", "remittance": "Invoice 2026-114",
			},
			wantErr: ErrInvalidPayload,
		},
		{
			name: "an amount above the ceiling is rejected",
			payload: map[string]any{
				"name": "Example Ltd", "iban": testIBAN, "amount": 1000000000,
			},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an amount below one cent is rejected",
			payload: map[string]any{"name": "Example Ltd", "iban": testIBAN, "amount": 0.001},
			wantErr: ErrInvalidPayload,
		},
		{
			name: "an over-long beneficiary name is rejected",
			payload: map[string]any{
				"name": strings.Repeat("n", epcMaxName+1), "iban": testIBAN,
			},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing name is rejected",
			payload: map[string]any{"iban": testIBAN},
			wantErr: ErrMissingField,
		},
		{
			name:    "a missing iban is rejected",
			payload: map[string]any{"name": "Example Ltd"},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"name": "x", "iban": testIBAN, "ibna": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

// TestEPCRejectsHostileText is the escaping case for a format that has no
// escaping: the fields are positional lines, so a line break in one of them
// would move every later field onto the wrong line and pay the wrong account.
func TestEPCRejectsHostileText(t *testing.T) {
	t.Parallel()

	_, err := epcBuilder{}.Build(map[string]any{
		"name": "Example Ltd", "iban": testIBAN, "remittance": hostileInput,
	})
	if err == nil {
		t.Fatal("Build accepted a remittance containing a line break")
	}

	// Without the break, the same delimiters are carried verbatim.
	assertHostileRoundTrip(t, epcBuilder{}, map[string]any{
		"name": "Example Ltd", "iban": testIBAN,
		"remittance": strings.ReplaceAll(hostileInput, "\n", " "),
	}, "remittance")
}

func TestEPCParse(t *testing.T) {
	t.Parallel()

	b := epcBuilder{}
	assertParsed(t, b, "BCD\n002\n1\nSCT\n\nExample Ltd\n"+testIBAN,
		map[string]any{"name": "Example Ltd", "iban": testIBAN})

	assertNotParsed(t, b,
		"BCD\n001\n1\nSCT\n\nExample Ltd\n"+testIBAN,             // an older version
		"BCD\n002\n2\nSCT\n\nExample Ltd\n"+testIBAN,             // a charset this builder does not emit
		"BCD\n002\n1\nSDD\n\nExample Ltd\n"+testIBAN,             // a direct debit, not a credit transfer
		"BCD\n002\n1\nSCT\n\nExample Ltd\nDE0000",                // a bad iban
		"BCD\n002\n1\nSCT\n\n\n"+testIBAN,                        // no beneficiary
		"BCD\n002\n1\nSCT\n\nExample Ltd\n"+testIBAN+"\n12.50",   // an amount with no currency
		"BCD\n002\n1\nSCT\n\nExample Ltd\n"+testIBAN+"\nEUR12.5", // not two decimals
		"BCD\n002\n1\nSCT",
	)
}

func TestIBANCheckDigits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		iban string
		want bool
	}{
		{testIBAN, true},
		{"GB82WEST12345698765432", true},
		{"FR1420041010050500013M02606", true},
		{"GB82WEST12345698765433", false}, // one digit out
		{"GB00WEST12345698765432", false}, // check digits zeroed
	} {
		if got := ibanCheckDigitsOK(tc.iban); got != tc.want {
			t.Errorf("ibanCheckDigitsOK(%q) = %t, want %t", tc.iban, got, tc.want)
		}
	}
}
