package locates

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Error carries whether a failed locate operation may have reached the broker.
// Ambiguous failures must keep the request's idempotency key for a safe retry;
// definitive broker rejections start a new logical attempt.
type Error struct {
	Err       error
	Ambiguous bool
}

func (e *Error) Error() string {
	if e == nil || e.Err == nil {
		return "locate operation failed"
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func MarkAmbiguous(err error) error {
	if err == nil {
		return nil
	}
	return &Error{Err: err, Ambiguous: true}
}

func IsAmbiguous(err error) bool {
	var locateErr *Error
	return errors.As(err, &locateErr) && locateErr.Ambiguous
}

// Eligibility is the Alpaca asset-cache view used by the locate workflow.
type Eligibility struct {
	BorrowStatus *string
	Shortable    *bool
	Marginable   *bool
	Tradable     *bool
}

type Quote struct {
	Symbol       string
	AvailableQty int64
	Price        string
	QuotedAt     time.Time
}

type QuoteError struct {
	Symbol  string
	Code    string
	Message string
}

type QuoteResult struct {
	Quotes []Quote
	Errors []QuoteError
}

type Request struct {
	Symbol         string
	Qty            int64
	LimitPrice     string
	AllOrNone      bool
	IdempotencyKey string
}

type Record struct {
	ID           string
	Symbol       string
	RequestedQty int64
	LimitPrice   string
	AllOrNone    bool
	Status       string
	CreatedAt    time.Time
	LocatedQty   int64
	LocatedPrice string
	TotalFee     string
	ExpiresAt    time.Time
}

type ListFilter struct {
	Status    string
	Symbol    string
	Start     string
	End       string
	Limit     int
	PageToken string
}

type Page struct {
	Locates       []Record
	NextPageToken string
}

const (
	StatusActive   = "active"
	StatusExpired  = "expired"
	StatusRejected = "rejected"
)

func (r Request) Validate() error {
	if strings.TrimSpace(r.Symbol) == "" {
		return fmt.Errorf("locate symbol is required")
	}
	if r.Qty <= 0 || r.Qty%100 != 0 {
		return fmt.Errorf("locate quantity must be a positive multiple of 100")
	}
	if !positiveDecimal(r.LimitPrice) {
		return fmt.Errorf("locate limit price must be a positive decimal")
	}
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return fmt.Errorf("locate idempotency key is required")
	}
	if utf8.RuneCountInString(r.IdempotencyKey) > 128 {
		return fmt.Errorf("locate idempotency key must be 128 characters or fewer")
	}
	return nil
}

func (f ListFilter) Validate() error {
	if f.Status != "" && !IsStatus(f.Status) {
		return fmt.Errorf("locate status must be active, expired, or rejected")
	}
	if f.Limit < 0 || f.Limit > 100 {
		return fmt.Errorf("locate limit must be between 0 and 100")
	}
	return nil
}

func IsStatus(status string) bool {
	switch status {
	case StatusActive, StatusExpired, StatusRejected:
		return true
	default:
		return false
	}
}

func positiveDecimal(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	digits := 0
	nonZero := false
	dot := false
	for _, r := range s {
		switch {
		case r == '.' && !dot:
			dot = true
		case r >= '0' && r <= '9':
			digits++
			nonZero = nonZero || r != '0'
		default:
			return false
		}
	}
	return digits > 0 && nonZero
}
