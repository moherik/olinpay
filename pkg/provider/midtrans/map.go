package midtrans

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// jakarta is the timezone Midtrans timestamps are expressed in. It falls back
// to a fixed UTC+7 zone when the tzdata is unavailable (minimal containers).
var jakarta = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*60*60)
}()

// orderTimeLayout is the absolute timestamp format Midtrans expects in
// custom_expiry.order_time, e.g. "2026-10-15 14:30:00 +0700".
const orderTimeLayout = "2006-01-02 15:04:05 -0700"

// maxExpiry caps the requested payment lifetime.
const maxExpiry = 180 * 24 * time.Hour

// MandiriChannel is the unified channel code that maps onto the echannel
// (Mandiri Bill Payment) product rather than a bank_transfer virtual account.
const MandiriChannel = "MANDIRI"

// qrisDefaultAcquirer is the QRIS acquirer used when none is configured.
const qrisDefaultAcquirer = "gopay"

// vaBanks maps the unified VA channel code onto the lowercase bank slug the
// Core API expects on a bank_transfer charge. Mandiri is handled separately via
// echannel and is therefore not listed here.
var vaBanks = map[string]string{
	"PERMATA": "permata",
	"BCA":     "bca",
	"BNI":     "bni",
	"BRI":     "bri",
	"CIMB":    "cimb",
	"DANAMON": "danamon",
	"BSI":     "bsi",
	"SEABANK": "seabank",
	"SAQU":    "saqu",
}

// ewalletChannels maps the unified e-wallet channel code onto the payment_type
// Midtrans expects.
var ewalletChannels = map[string]string{
	"GOPAY":     "gopay",
	"SHOPEEPAY": "shopeepay",
}

// retailChannels maps the unified retail outlet code onto the cstore store
// slug Midtrans expects.
var retailChannels = map[string]string{
	"INDOMARET": "indomaret",
	"ALFAMART":  "alfamart",
}

// mapTransactionStatus translates Midtrans' textual transaction_status (plus
// fraud_status for card-style captures) onto a unified [provider.Status]:
//
//	capture + fraud accept  -> paid
//	settlement              -> paid
//	pending / authorize     -> pending
//	deny / failure          -> failed
//	cancel                  -> cancelled
//	expire                  -> expired
//	refund / chargeback ... -> refunded
func mapTransactionStatus(status, fraud string) provider.Status {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "capture":
		if strings.EqualFold(strings.TrimSpace(fraud), "deny") {
			return provider.StatusFailed
		}
		return provider.StatusPaid
	case "settlement":
		return provider.StatusPaid
	case "pending", "authorize":
		return provider.StatusPending
	case "deny", "failure":
		return provider.StatusFailed
	case "cancel":
		return provider.StatusCancelled
	case "expire":
		return provider.StatusExpired
	case "refund", "partial_refund", "chargeback", "partial_chargeback":
		return provider.StatusRefunded
	default:
		return provider.StatusUnknown
	}
}

// eventTypeFor maps a MIDTRANS payment_type onto the unified webhook event
// name. It returns "" for payment types this adapter does not model.
func eventTypeFor(paymentType string) string {
	switch strings.ToLower(strings.TrimSpace(paymentType)) {
	case "bank_transfer", "echannel":
		return EventVAPayment
	case "qris":
		return EventQRISPayment
	case "gopay", "shopeepay":
		return EventEWalletPayment
	case "cstore":
		return EventRetailPayment
	default:
		return ""
	}
}

// classifyStatus maps a Midtrans response onto a contract sentinel so the core
// engine can react with errors.Is; a 2xx HTTP status or status_code yields nil.
// It prefers the HTTP status and falls back to the numeric status_code, because
// Midtrans reports business errors with HTTP 200 and a non-2xx status_code.
func classifyStatus(httpStatus int, code string) error {
	switch {
	case (httpStatus >= 200 && httpStatus < 300) || strings.HasPrefix(code, "2"):
		return nil
	case httpStatus == 401 || httpStatus == 403,
		strings.HasPrefix(code, "401"), strings.HasPrefix(code, "403"):
		return provider.ErrUnauthorized
	case httpStatus == 404, strings.HasPrefix(code, "404"):
		return provider.ErrNotFound
	case httpStatus == 409,
		strings.HasPrefix(code, "406"), strings.HasPrefix(code, "409"):
		return provider.ErrDuplicateReference
	case httpStatus == 400 || httpStatus == 422, strings.HasPrefix(code, "4"):
		return provider.ErrInvalidRequest
	default:
		return provider.ErrGateway
	}
}

// checkStatus returns a [*provider.Error] when the response status_code is not
// a 2xx. Midtrans reports most business failures this way even over HTTP 200.
func checkStatus(op string, r apiResponse) error {
	if r.success() {
		return nil
	}
	message := firstNonEmpty(r.StatusMessage, firstValidationMessage(r.ValidationMessages), r.StatusCode)
	return &provider.Error{
		Provider: Name,
		Op:       op,
		Code:     strings.TrimSpace(r.StatusCode),
		Message:  message,
		Err:      classifyStatus(0, r.StatusCode),
	}
}

// firstValidationMessage renders the first validation message, if any.
func firstValidationMessage(messages []validationMessage) string {
	for _, m := range messages {
		if msg := strings.TrimSpace(m.Message); msg != "" {
			if field := strings.TrimSpace(m.Field); field != "" {
				return field + ": " + msg
			}
			return msg
		}
	}
	return ""
}

// paymentDetails derives the unified payment code and payer actions from a
// charge/status response. Exactly one of the displayed identifiers is chosen as
// the code so callers can render it without inspecting every action.
func paymentDetails(out *chargeResponse) (code string, actions []provider.PaymentAction) {
	switch strings.ToLower(strings.TrimSpace(out.PaymentType)) {
	case "qris":
		if qr := strings.TrimSpace(out.QRString); qr != "" {
			code = qr
			actions = append(actions, provider.PaymentAction{
				Type: provider.ActionQRCode, Value: qr, Description: "QRIS payload to render",
			})
		}
		for _, a := range out.Actions {
			if strings.EqualFold(strings.TrimSpace(a.Name), "generate-qr-code") && strings.TrimSpace(a.URL) != "" {
				actions = append(actions, provider.PaymentAction{
					Type: provider.ActionURL, Value: strings.TrimSpace(a.URL), Description: "QRIS image",
				})
			}
		}
	case "gopay", "shopeepay":
		for _, a := range out.Actions {
			url := strings.TrimSpace(a.URL)
			if url == "" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(a.Name), "deeplink-redirect") {
				actions = append(actions, provider.PaymentAction{
					Type: provider.ActionDeeplink, Value: url, Description: "Open the e-wallet app",
				})
				continue
			}
			actions = append(actions, provider.PaymentAction{
				Type: provider.ActionURL, Value: url, Description: "Complete the payment in the e-wallet",
			})
		}
	case "cstore":
		if pc := strings.TrimSpace(out.PaymentCode); pc != "" {
			code = pc
			actions = append(actions, provider.PaymentAction{
				Type: provider.ActionDisplay, Value: pc, Description: "Retail outlet payment code",
			})
		}
	case "echannel":
		biller := strings.TrimSpace(out.BillerCode)
		key := strings.TrimSpace(out.BillKey)
		if biller != "" || key != "" {
			code = biller + key
			actions = append(actions, provider.PaymentAction{
				Type: provider.ActionDisplay, Value: code,
				Description: "Mandiri bill payment code (biller code + bill key)",
			})
		}
	default: // bank_transfer and unknown shapes
		for _, va := range out.VANumbers {
			if vaNo := strings.TrimSpace(va.VANumber); vaNo != "" {
				code = vaNo
				actions = append(actions, provider.PaymentAction{
					Type: provider.ActionDisplay, Value: vaNo, Description: "Virtual account number",
				})
				break
			}
		}
		if code == "" {
			if permata := strings.TrimSpace(out.PermataVANumber); permata != "" {
				code = permata
				actions = append(actions, provider.PaymentAction{
					Type: provider.ActionDisplay, Value: permata, Description: "Virtual account number",
				})
			}
		}
	}
	return code, actions
}

// parseAmount converts a Midtrans decimal amount string ("10000.00") into the
// unified integer smallest-unit amount (rupiah for IDR). Sub-unit fractions are
// rejected rather than silently rounded: IDR has no practical fractional unit.
func parseAmount(value string) (provider.Amount, error) {
	raw := strings.TrimSpace(value)
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
		return 0, fmt.Errorf("midtrans: invalid amount %q: %w", value, err)
	}
	if strings.Trim(fraction, "0") != "" {
		return 0, fmt.Errorf("midtrans: amount %q has sub-unit precision", value)
	}
	return n, nil
}

// amountOr parses the gateway amount, falling back to the value captured at
// creation time when the field is absent or malformed.
func amountOr(value string, fallback provider.Amount) provider.Amount {
	if parsed, err := parseAmount(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

// parseTime parses a Midtrans timestamp. Midtrans renders Core API timestamps
// as "2006-01-02 15:04:05" in Jakarta time (no offset); ISO-8601 forms are also
// accepted. It returns nil when the value is empty or unparseable.
func parseTime(value string) *time.Time {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, orderTimeLayout, "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, raw, jakarta); err == nil {
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

// customExpiryOf builds the custom_expiry object for a requested lifetime,
// choosing the largest unit that divides it evenly.
func customExpiryOf(d time.Duration) *customExpiry {
	if d <= 0 {
		return nil
	}
	const day = 24 * time.Hour
	unit, divisor := "second", time.Second
	switch {
	case d%day == 0:
		unit, divisor = "day", day
	case d%time.Hour == 0:
		unit, divisor = "hour", time.Hour
	case d%time.Minute == 0:
		unit, divisor = "minute", time.Minute
	}
	return &customExpiry{
		OrderTime:      time.Now().In(jakarta).Format(orderTimeLayout),
		ExpiryDuration: int(d / divisor),
		Unit:           unit,
	}
}

// customerDetailsFrom splits a unified customer into Midtrans' first/last name
// fields. It returns nil when there is nothing to send.
func customerDetailsFrom(c provider.Customer) *customerDetails {
	first, last := splitName(c.Name)
	if first == "" && c.Email == "" && c.Phone == "" {
		return nil
	}
	return &customerDetails{
		FirstName: first,
		LastName:  last,
		Email:     strings.TrimSpace(c.Email),
		Phone:     strings.TrimSpace(c.Phone),
	}
}

// splitName divides a display name into first and last name at the first
// whitespace, matching Midtrans' customerDetails shape.
func splitName(name string) (first, last string) {
	fields := strings.Fields(name)
	switch len(fields) {
	case 0:
		return "", ""
	case 1:
		return fields[0], ""
	default:
		return fields[0], strings.Join(fields[1:], " ")
	}
}

// itemDetailsFrom converts unified line items into Midtrans item_details.
func itemDetailsFrom(items []provider.Item) []itemDetail {
	if len(items) == 0 {
		return nil
	}
	out := make([]itemDetail, 0, len(items))
	for _, it := range items {
		out = append(out, itemDetail{
			ID:       strings.TrimSpace(it.ID),
			Price:    it.Price,
			Quantity: it.Quantity,
			Name:     it.Name,
		})
	}
	return out
}

// descriptionOr returns the payment description, falling back to the merchant
// reference so gateway fields that require text are never empty.
func descriptionOr(req *provider.CreatePaymentRequest) string {
	if d := strings.TrimSpace(req.Description); d != "" {
		return d
	}
	return req.ReferenceID
}

// addressID picks the identifier Midtrans should address a status/cancel call
// by. The Core API accepts either the order_id or the transaction_id in the
// path; the merchant reference (order_id) is preferred because this adapter
// always sets it at creation.
func addressID(reference, providerReference string) string {
	return firstNonEmpty(reference, providerReference)
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
