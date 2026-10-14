package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/moherik/openpay.id/internal/config"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/id"
	"github.com/moherik/openpay.id/internal/repository"
)

// Outbound callback header names. Integrators should verify
// [HeaderSignature] against their webhook secret.
const (
	HeaderEvent     = "X-OpenPay-Event"
	HeaderDelivery  = "X-OpenPay-Delivery"
	HeaderTimestamp = "X-OpenPay-Timestamp"
	HeaderSignature = "X-OpenPay-Signature"
)

// dispatcherBatchSize is how many due deliveries are processed per poll.
const dispatcherBatchSize = 50

// Dispatcher is the outbound side of the webhook engine: it persists each
// merchant notification and delivers it with exponential backoff, so a crash or
// a temporarily unreachable merchant never loses an event.
type Dispatcher struct {
	deliveries repository.WebhookDeliveryRepository
	merchants  repository.MerchantRepository
	client     *http.Client
	cfg        config.WebhookConfig
	log        *slog.Logger

	now   func() time.Time
	newID func() string
}

// NewDispatcher builds the outbound dispatcher.
func NewDispatcher(
	deliveries repository.WebhookDeliveryRepository,
	merchants repository.MerchantRepository,
	cfg config.WebhookConfig,
	log *slog.Logger,
) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 6
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 5 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 5 * time.Minute
	}
	if cfg.WorkerInterval <= 0 {
		cfg.WorkerInterval = 5 * time.Second
	}
	return &Dispatcher{
		deliveries: deliveries,
		merchants:  merchants,
		client:     &http.Client{Timeout: cfg.Timeout},
		cfg:        cfg,
		log:        log,
		now:        time.Now,
		newID:      func() string { return id.NewPrefixed("whd") },
	}
}

// Notify implements core.Notifier. It resolves the merchant's callback URL and
// enqueues the message. Merchants without a callback URL are silently skipped.
func (d *Dispatcher) Notify(ctx context.Context, tx *domain.Transaction) error {
	if tx == nil {
		return nil
	}
	merchant, err := d.merchants.GetByID(ctx, tx.MerchantID)
	if err != nil {
		return fmt.Errorf("webhook: resolving merchant %s: %w", tx.MerchantID, err)
	}
	if merchant.CallbackURL == "" {
		d.log.Debug("merchant has no callback url; skipping notification",
			"merchant_id", tx.MerchantID)
		return nil
	}

	now := d.now()
	msg := domain.WebhookMessage{
		ID:        d.newID(),
		Event:     tx.EventType(),
		CreatedAt: now,
		Data:      tx,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("webhook: encoding message: %w", err)
	}

	delivery := &domain.WebhookDelivery{
		ID:            msg.ID,
		TransactionID: tx.ID,
		MerchantID:    tx.MerchantID,
		EventType:     msg.Event,
		URL:           merchant.CallbackURL,
		Payload:       payload,
		Status:        domain.DeliveryPending,
		MaxAttempts:   d.cfg.MaxAttempts,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := d.deliveries.Create(ctx, delivery); err != nil {
		return err
	}
	d.log.Info("notification queued",
		"delivery_id", delivery.ID, "merchant_id", tx.MerchantID, "event", msg.Event)
	return nil
}

// Run polls the delivery queue until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.WorkerInterval)
	defer ticker.Stop()

	d.log.Info("webhook dispatcher started", "interval", d.cfg.WorkerInterval.String())
	d.process(ctx)

	for {
		select {
		case <-ctx.Done():
			d.log.Info("webhook dispatcher stopped")
			return
		case <-ticker.C:
			d.process(ctx)
		}
	}
}

func (d *Dispatcher) process(ctx context.Context) {
	due, err := d.deliveries.Due(ctx, d.now(), dispatcherBatchSize)
	if err != nil {
		d.log.Error("listing due webhook deliveries failed", "error", err.Error())
		return
	}
	for _, delivery := range due {
		if ctx.Err() != nil {
			return
		}
		d.attempt(ctx, delivery)
	}
}

func (d *Dispatcher) attempt(ctx context.Context, delivery *domain.WebhookDelivery) {
	dctx, cancel := ContextWithTimeout(ctx, d.cfg.Timeout)
	defer cancel()

	secret := d.merchantSecret(dctx, delivery.MerchantID)

	req, err := http.NewRequestWithContext(dctx, http.MethodPost, delivery.URL, bytes.NewReader(delivery.Payload))
	if err != nil {
		d.recordFailure(ctx, delivery, "building request: "+err.Error())
		return
	}
	ts := strconv.FormatInt(d.now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "openpay.id-webhook/1.0")
	req.Header.Set(HeaderEvent, delivery.EventType)
	req.Header.Set(HeaderDelivery, delivery.ID)
	req.Header.Set(HeaderTimestamp, ts)
	if secret != "" {
		req.Header.Set(HeaderSignature, Sign(secret, ts, delivery.Payload))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		d.recordFailure(ctx, delivery, err.Error())
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		d.recordSuccess(ctx, delivery, resp.StatusCode)
		return
	}
	d.recordFailure(ctx, delivery, fmt.Sprintf("merchant responded with status %d", resp.StatusCode))
}

func (d *Dispatcher) recordSuccess(ctx context.Context, delivery *domain.WebhookDelivery, status int) {
	now := d.now()
	delivery.Attempts++
	delivery.Status = domain.DeliveryDelivered
	delivery.LastError = ""
	delivery.LastAttemptAt = &now
	delivery.NextAttemptAt = nil
	delivery.UpdatedAt = now
	if err := d.deliveries.Update(ctx, delivery); err != nil {
		d.log.Error("persisting webhook success failed", "delivery_id", delivery.ID, "error", err.Error())
		return
	}
	d.log.Info("notification delivered",
		"delivery_id", delivery.ID, "status_code", status, "attempts", delivery.Attempts)
}

func (d *Dispatcher) recordFailure(ctx context.Context, delivery *domain.WebhookDelivery, reason string) {
	now := d.now()
	delivery.Attempts++
	delivery.LastError = reason
	delivery.LastAttemptAt = &now
	delivery.UpdatedAt = now

	if delivery.Attempts >= delivery.MaxAttempts {
		delivery.Status = domain.DeliveryFailed
		delivery.NextAttemptAt = nil
	} else {
		next := now.Add(d.backoff(delivery.Attempts))
		delivery.NextAttemptAt = &next
	}
	if err := d.deliveries.Update(ctx, delivery); err != nil {
		d.log.Error("persisting webhook failure failed", "delivery_id", delivery.ID, "error", err.Error())
	}
	d.log.Warn("notification attempt failed",
		"delivery_id", delivery.ID, "attempts", delivery.Attempts, "reason", reason)
}

// backoff returns the delay before the next attempt: base * 2^(attempts-1),
// capped at MaxBackoff.
func (d *Dispatcher) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := d.cfg.BaseBackoff
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= d.cfg.MaxBackoff {
			return d.cfg.MaxBackoff
		}
	}
	if delay > d.cfg.MaxBackoff {
		return d.cfg.MaxBackoff
	}
	return delay
}

func (d *Dispatcher) merchantSecret(ctx context.Context, merchantID string) string {
	m, err := d.merchants.GetByID(ctx, merchantID)
	if err != nil {
		d.log.Warn("resolving merchant secret failed", "merchant_id", merchantID, "error", err.Error())
		return ""
	}
	return m.WebhookSecret
}
