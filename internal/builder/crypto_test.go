package builder

import "testing"

// A well-formed bech32 address, used only for its shape.
const testBTCAddress = "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"

func TestCryptoBuild(t *testing.T) {
	t.Parallel()

	b := cryptoBuilder{}
	runBuildCases(t, b, []buildCase{
		{
			name: "a bitcoin payment request",
			payload: map[string]any{
				"coin": "bitcoin", "address": testBTCAddress, "amount": 0.015,
				"label": "Example Ltd", "message": "Invoice 2026-114",
			},
			want: "bitcoin:" + testBTCAddress + "?amount=0.015&label=Example%20Ltd" +
				"&message=Invoice%202026-114",
		},
		{
			name:    "the coin defaults to bitcoin",
			payload: map[string]any{"address": testBTCAddress},
			want:    "bitcoin:" + testBTCAddress,
		},
		{
			name:    "ethereum uses the same shape",
			payload: map[string]any{"coin": "ETHEREUM", "address": "0x0123456789abcdef0123"},
			want:    "ethereum:0x0123456789abcdef0123",
		},
		{
			name:    "a zero amount is rejected",
			payload: map[string]any{"address": testBTCAddress, "amount": 0},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a negative amount is rejected",
			payload: map[string]any{"address": testBTCAddress, "amount": -1},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an unknown coin is rejected",
			payload: map[string]any{"coin": "dogecoin", "address": testBTCAddress},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "an address with punctuation is rejected",
			payload: map[string]any{"address": "bc1q?amount=99"},
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "a missing address is rejected",
			payload: map[string]any{"amount": 1},
			wantErr: ErrMissingField,
		},
		{
			name:    "an unknown field is rejected",
			payload: map[string]any{"address": testBTCAddress, "amont": "typo"},
			wantErr: ErrInvalidPayload,
		},
	})
}

func TestCryptoEscapesAHostileMessage(t *testing.T) {
	t.Parallel()

	assertHostileRoundTrip(t, cryptoBuilder{}, map[string]any{
		"address": testBTCAddress, "message": hostileInput,
	}, "message")
}

func TestCryptoParse(t *testing.T) {
	t.Parallel()

	b := cryptoBuilder{}
	assertParsed(t, b, "BITCOIN:"+testBTCAddress,
		map[string]any{"coin": "bitcoin", "address": testBTCAddress})
	assertParsed(t, b, "litecoin:"+testBTCAddress+"?amount=1.5",
		map[string]any{"coin": "litecoin", "address": testBTCAddress, "amount": 1.5})

	assertNotParsed(t, b,
		"dogecoin:"+testBTCAddress,               // a coin this builder does not emit
		"bitcoin:short",                          // not an address shape
		"bitcoin:"+testBTCAddress+"?amount=0",    // an amount Build would reject
		"bitcoin:"+testBTCAddress+"?amount=1.50", // not the shortest form
		"bitcoin:"+testBTCAddress+"?r=https://x", // a BIP-72 parameter not modelled
	)
}
