package model

import "time"

// PaymentMethod is what we keep about a card, which is deliberately much less
// than what the customer gave us.
//
// The original model held card_number and cvv as ordinary strings and integers,
// serialised into every response. That is vulnerability #12: the full number and
// the security code readable by anyone who could reach a listing, and stored
// indefinitely in a database nobody had designed for that data.
//
// A PAN is sensitive authentication data. PCI DSS allows storing it only
// encrypted, with the key under separate control, and this application has no
// payment processor and no reason to keep it. The card number is used once, over
// TLS, to derive the brand and the last four digits, and is then discarded. The
// CVV is never stored at all, in any form, because the standard forbids keeping
// it after authorisation rather than merely protecting it.
type PaymentMethod struct {
	ID          int64     `json:"id,string"`
	UserID      int64     `json:"user_id,string"`
	Brand       string    `json:"brand"`
	Last4       string    `json:"last4"`
	ExpiryMonth int       `json:"expiry_month"`
	ExpiryYear  int       `json:"expiry_year"`
	CreatedAt   time.Time `json:"created_at"`

	// CardNumber exists only on the way in and is never part of a response.
	//
	// It is not stored, so it cannot be read back, and json:"-" guarantees that
	// even an accidental use of this struct in a response cannot serialise it.
	// Only the handler that creates a card ever sets it.
	CardNumber string `json:"-"`

	// CardHolder is accepted and dropped. It is personal data that a receipt does
	// not need, so there is no reason to keep it.
	CardHolder string `json:"-"`
}
