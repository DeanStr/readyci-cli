package preflightapi

import "time"

type CreatePrepaidCheckoutRequest struct {
	Packs             int    `json:"packs"`
	CheckoutAttemptID string `json:"checkout_attempt_id"`
}

type PrepaidOrderSummary struct {
	ID                string     `json:"id"`
	Packs             int        `json:"packs"`
	Status            string     `json:"status"`
	CreatedAt         time.Time  `json:"created_at"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	CheckoutAttemptID string     `json:"checkout_attempt_id,omitempty"`
	CheckoutExpiresAt *time.Time `json:"checkout_expires_at,omitempty"`
}

type PrepaidOfferResponse struct {
	Enabled            bool                  `json:"enabled"`
	BillingHoldActive  bool                  `json:"billing_hold_active"`
	SlotMinutesPerPack int                   `json:"slot_minutes_per_pack"`
	PriceCentsPerPack  int                   `json:"price_cents_per_pack"`
	Currency           string                `json:"currency"`
	MaxPacks           int                   `json:"max_packs"`
	Orders             []PrepaidOrderSummary `json:"orders"`
}
