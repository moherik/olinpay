package xendit

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// vaChannels maps the unified VA channel codes onto Xendit channel_code values.
// Availability of each bank depends on the merchant's Xendit account.
var vaChannels = map[string]string{
	"BCA":      "BCA_VIRTUAL_ACCOUNT",
	"BNI":      "BNI_VIRTUAL_ACCOUNT",
	"BRI":      "BRI_VIRTUAL_ACCOUNT",
	"MANDIRI":  "MANDIRI_VIRTUAL_ACCOUNT",
	"PERMATA":  "PERMATA_VIRTUAL_ACCOUNT",
	"CIMB":     "CIMB_VIRTUAL_ACCOUNT",
	"BSI":      "BSI_VIRTUAL_ACCOUNT",
	"BNC":      "BNC_VIRTUAL_ACCOUNT",
	"MUAMALAT": "MUAMALAT_VIRTUAL_ACCOUNT",
	"BJB":      "BJB_VIRTUAL_ACCOUNT",
}

// ewalletChannels maps e-wallet channel codes onto Xendit channel_code values.
var ewalletChannels = map[string]string{
	"OVO":       "OVO",
	"DANA":      "DANA",
	"SHOPEEPAY": "SHOPEEPAY",
	"LINKAJA":   "LINKAJA",
	"JENIUSPAY": "JENIUSPAY",
	"ASTRAPAY":  "ASTRAPAY",
	"GOPAY":     "GOPAY",
	"NEXCASH":   "NEXCASH",
}

// retailChannels maps over-the-counter channel codes onto Xendit channel_code
// values. These use the reusable payment code product.
var retailChannels = map[string]string{
	"ALFAMART":  "ALFAMART",
	"INDOMARET": "INDOMARET",
}

// paylaterChannels maps pay-later channel codes onto Xendit channel_code values.
var paylaterChannels = map[string]string{
	"AKULAKU":  "AKULAKU",
	"KREDIVO":  "KREDIVO",
	"ATOME":    "ATOME",
	"INDODANA": "INDODANA",
}

// channelCard is the unified channel for card payments.
const channelCard = "CARD"

// mapStatus translates a Xendit payment request or payment status into a
// unified [provider.Status]. Both status vocabularies share these values.
func mapStatus(status string) provider.Status {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCEEDED":
		return provider.StatusPaid
	case "REQUIRES_ACTION", "ACCEPTING_PAYMENTS", "AUTHORIZED", "PENDING":
		return provider.StatusPending
	case "FAILED":
		return provider.StatusFailed
	case "CANCELED":
		return provider.StatusCancelled
	case "EXPIRED":
		return provider.StatusExpired
	default:
		return provider.StatusUnknown
	}
}

// parseAmount converts a Xendit numeric amount into the contract's integer
// smallest-unit amount (rupiah for IDR).
//
// Xendit renders IDR amounts as whole numbers; a sub-unit fraction is rejected
// rather than silently rounded, since IDR has no practical fractional unit.
func parseAmount(value json.Number) (provider.Amount, error) {
	raw := strings.TrimSpace(value.String())
	if raw == "" {
		return 0, nil
	}
	units, fraction, _ := strings.Cut(raw, ".")
	units = strings.TrimLeft(units, "+")
	if units == "" {
		units = "0"
	}
	n, err := strconv.ParseInt(units, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("xendit: invalid amount %q: %w", raw, err)
	}
	if strings.Trim(fraction, "0") != "" {
		return 0, fmt.Errorf("xendit: amount %q has sub-unit precision", raw)
	}
	return n, nil
}

// amountOr parses the gateway amount, falling back to the value captured at
// creation time when the field is absent or malformed.
func amountOr(value json.Number, fallback provider.Amount) provider.Amount {
	if parsed, err := parseAmount(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

// parseTime parses a Xendit ISO-8601 timestamp. It returns nil when the value
// is empty or unparseable so callers can treat the field as optional.
func parseTime(value string) *time.Time {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t
		}
	}
	return nil
}

// paidAtIfPaid keeps a settlement timestamp only for actually settled payments.
func paidAtIfPaid(status provider.Status, t *time.Time) *time.Time {
	if status == provider.StatusPaid {
		return t
	}
	return nil
}

// lastCaptureTime returns the timestamp of the most recent capture, if any.
func lastCaptureTime(captures []capture) *time.Time {
	if len(captures) == 0 {
		return nil
	}
	return parseTime(captures[len(captures)-1].CaptureTimestamp)
}

// classifyStatus maps an unsuccessful Xendit response onto a contract sentinel
// so the core engine can react with errors.Is.
func classifyStatus(httpStatus int, code string) error {
	switch {
	case httpStatus == 401 || httpStatus == 403:
		return provider.ErrUnauthorized
	case httpStatus == 404:
		return provider.ErrNotFound
	case httpStatus == 409:
		return provider.ErrDuplicateReference
	case httpStatus == 400 || httpStatus == 422:
		return provider.ErrInvalidRequest
	}
	switch {
	case strings.HasPrefix(code, "401"), strings.HasPrefix(code, "403"):
		return provider.ErrUnauthorized
	case strings.HasPrefix(code, "404"):
		return provider.ErrNotFound
	case strings.HasPrefix(code, "409"):
		return provider.ErrDuplicateReference
	case strings.HasPrefix(code, "4"):
		return provider.ErrInvalidRequest
	default:
		return provider.ErrGateway
	}
}

// mapActions translates Xendit's "actions" array into unified payment actions,
// returning the first human-facing payment code it encounters.
func mapActions(actions []action) (mapped []provider.PaymentAction, code string) {
	for _, a := range actions {
		value := strings.TrimSpace(a.Value)
		if value == "" {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(a.Descriptor)) {
		case descriptorVANumber:
			mapped = append(mapped, provider.PaymentAction{
				Type: provider.ActionDisplay, Value: value, Description: "Virtual account number",
			})
			if code == "" {
				code = value
			}
		case descriptorPaymentCode:
			mapped = append(mapped, provider.PaymentAction{
				Type: provider.ActionDisplay, Value: value, Description: "Payment code",
			})
			if code == "" {
				code = value
			}
		case descriptorQRString:
			mapped = append(mapped, provider.PaymentAction{
				Type: provider.ActionQRCode, Value: value, Description: "QR code payload",
			})
			if code == "" {
				code = value
			}
		case descriptorWebURL:
			mapped = append(mapped, provider.PaymentAction{
				Type: provider.ActionURL, Value: value, Description: "Payment page",
			})
		case descriptorDeeplink:
			mapped = append(mapped, provider.PaymentAction{
				Type: provider.ActionDeeplink, Value: value, Description: "Open the payment app",
			})
		default:
			// Fall back to the action type when the descriptor is unknown.
			switch strings.ToUpper(strings.TrimSpace(a.Type)) {
			case actionRedirect:
				mapped = append(mapped, provider.PaymentAction{
					Type: provider.ActionURL, Value: value, Description: "Payment page",
				})
			case actionPresent:
				mapped = append(mapped, provider.PaymentAction{
					Type: provider.ActionDisplay, Value: value,
				})
				if code == "" {
					code = value
				}
			}
		}
	}
	return mapped, code
}

// channelPropertyString returns the first string property present under one of
// the given keys.
func channelPropertyString(props map[string]any, keys ...string) string {
	for _, key := range keys {
		if raw, ok := props[key]; ok {
			if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

// expiresAt extracts an expiry from the echoed channel_properties, if present.
func expiresAt(props map[string]any) *time.Time {
	return parseTime(channelPropertyString(props, "expires_at"))
}

// customerName returns the customer's display name, if any.
func customerName(c provider.Customer) string {
	return strings.TrimSpace(c.Name)
}

// descriptionOr returns the payment description, falling back to the merchant
// reference so gateway fields that require text are never empty.
func descriptionOr(req *provider.CreatePaymentRequest) string {
	if d := strings.TrimSpace(req.Description); d != "" {
		return d
	}
	return req.ReferenceID
}

// metadataForXendit strips the adapter's control keys so they are not leaked
// into the gateway's opaque metadata object. It returns nil when nothing is
// left.
func metadataForXendit(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		switch k {
		case "payment_token_id", "card_token", "channel_properties",
			"success_return_url", "failure_return_url":
			continue
		default:
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mergeChannelProperties overlays an optional JSON object supplied through
// metadata["channel_properties"] onto the generated properties, letting callers
// pass channel-specific fields (e.g. a wallet's account number). Invalid JSON is
// reported rather than silently dropped so a typo cannot turn into a confusing
// gateway rejection.
func mergeChannelProperties(props map[string]any, raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return props, nil
	}
	extra := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &extra); err != nil {
		return nil, fmt.Errorf("xendit: metadata channel_properties is not a JSON object: %w", err)
	}
	if props == nil {
		props = make(map[string]any, len(extra))
	}
	for k, v := range extra {
		props[k] = v
	}
	return props, nil
}

// firstNonEmpty returns the first argument that is not blank.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// truncate bounds an error message taken from a raw gateway response.
func truncate(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
