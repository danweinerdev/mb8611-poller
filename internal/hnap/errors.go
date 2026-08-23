package hnap

import "errors"

var (
	ErrLockedOut  = errors.New("hnap: modem is in lockout")
	ErrAuthFailed = errors.New("hnap: authentication failed")
	ErrUnauth     = errors.New("hnap: UN-AUTH response")
)
