package builder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Crypto is the registry name of the cryptocurrency-payment builder.
const Crypto = "crypto"

func init() { Register(cryptoBuilder{}) }

// CryptoPayload carries a payment request.
type CryptoPayload struct {
	Coin    string  `json:"coin"`
	Address string  `json:"address"`
	Amount  float64 `json:"amount"`
	Label   string  `json:"label"`
	Message string  `json:"message"`
}

// cryptoCoins are the schemes this builder will emit. Each is the URI scheme
// registered by that chain's reference wallet.
var cryptoCoins = map[string]bool{
	"bitcoin": true, "ethereum": true, "litecoin": true,
}

// cryptoAddress is deliberately a shape check, not a chain-specific one. Base58,
// bech32 and 0x-hex addresses are all alphanumeric; validating a checksum would
// mean three chain-specific implementations that go stale as address formats
// evolve, and a wallet re-validates the address anyway before it will send.
var cryptoAddress = regexp.MustCompile(`^[A-Za-z0-9]{10,110}$`)

// cryptoBuilder emits a BIP-21 style payment URI.
//
// BIP-21 defines "bitcoin:<address>?amount=&label=&message=". Litecoin adopted
// it verbatim. Ethereum did not: EIP-681 spells the same intent as
// "ethereum:<address>@<chain>?value=<wei>", with the amount in wei rather than
// ether and an optional contract call. Wallets overwhelmingly accept the
// BIP-21 shape for all three, and the full EIP-681 grammar — chain ids,
// function calls, unit suffixes — is out of scope here, so the BIP-21 shape is
// what gets emitted. Anyone needing EIP-681 proper should use the raw builder.
type cryptoBuilder struct{}

func (cryptoBuilder) Name() string { return Crypto }

func (cryptoBuilder) Fields() []Field {
	return []Field{
		{
			Name: "coin", Type: TypeString,
			Description: "bitcoin, ethereum or litecoin; defaults to bitcoin",
			Example:     "bitcoin",
		},
		{
			Name: "address", Type: TypeString, Required: true,
			Description: "the receiving address", Example: "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq",
		},
		{
			Name: "amount", Type: TypeNumber,
			Description: "amount in the chain's main unit; must be positive", Example: "0.015",
		},
		{
			Name: "label", Type: TypeString,
			Description: "who is being paid", Example: "Example Ltd",
		},
		{
			Name: "message", Type: TypeString,
			Description: "what the payment is for", Example: "Invoice 2026-114",
		},
	}
}

func (b cryptoBuilder) Build(payload any) (string, error) {
	m, err := payloadMap(payload, b.Fields())
	if err != nil {
		return "", err
	}

	v, err := readStrings(m, []string{"coin", "address", "label", "message"})
	if err != nil {
		return "", err
	}

	coin := strings.ToLower(v["coin"])
	if coin == "" {
		coin = "bitcoin"
	}
	if !cryptoCoins[coin] {
		return "", fmt.Errorf("%w: coin %q: expected bitcoin, ethereum or litecoin",
			ErrInvalidPayload, v["coin"])
	}
	if v["address"] == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingField, "address")
	}
	if !cryptoAddress.MatchString(v["address"]) {
		return "", fmt.Errorf("%w: address must be 10 to 110 letters and digits",
			ErrInvalidPayload)
	}

	amount, hasAmount, err := number(m, "amount")
	if err != nil {
		return "", err
	}
	if hasAmount && amount <= 0 {
		return "", fmt.Errorf("%w: amount %s: expected a positive number",
			ErrInvalidPayload, formatNumber(amount))
	}

	out := coin + ":" + v["address"]
	var params []string
	if hasAmount {
		params = append(params, "amount="+formatNumber(amount))
	}
	if v["label"] != "" {
		params = append(params, "label="+pctEscape(v["label"]))
	}
	if v["message"] != "" {
		params = append(params, "message="+pctEscape(v["message"]))
	}
	if len(params) > 0 {
		out += "?" + strings.Join(params, "&")
	}
	return out, nil
}

func (cryptoBuilder) Parse(raw string) (any, bool) {
	scheme, rest, found := strings.Cut(raw, ":")
	if !found {
		return nil, false
	}
	coin := strings.ToLower(scheme)
	if !cryptoCoins[coin] {
		return nil, false
	}

	address, query, _ := strings.Cut(rest, "?")
	if !cryptoAddress.MatchString(address) {
		return nil, false
	}

	params, ok := parseURIQuery(query, "amount", "label", "message")
	if !ok {
		return nil, false
	}

	out := map[string]any{"coin": coin, "address": address}
	if text, present := params["amount"]; present {
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil || amount <= 0 || formatNumber(amount) != text {
			return nil, false
		}
		out["amount"] = amount
	}
	for _, key := range []string{"label", "message"} {
		if text, present := params[key]; present {
			out[key] = text
		}
	}
	if !trimmedValues(out) {
		return nil, false
	}
	return out, true
}
