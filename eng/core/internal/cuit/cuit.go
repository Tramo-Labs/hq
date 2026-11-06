// Package cuit validates Argentine CUIT and CUIL numbers: 11 digits whose
// last one is a mod 11 check digit over the first ten with weights
// 5 4 3 2 7 6 5 4 3 2. A result of 11 maps to 0; a result of 10 is never
// issued and is rejected.
package cuit

import (
	"errors"
	"strconv"
)

// ErrInvalid is returned for anything that is not a well formed CUIT.
var ErrInvalid = errors.New("invalid cuit")

var weights = [10]int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}

// Parse validates an 11 digit string (no dashes) and returns it as a number.
func Parse(s string) (int64, error) {
	if len(s) != 11 {
		return 0, ErrInvalid
	}
	for i := 0; i < 11; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrInvalid
		}
	}
	sum := 0
	for i := 0; i < 10; i++ {
		sum += int(s[i]-'0') * weights[i]
	}
	check := 11 - sum%11
	switch check {
	case 11:
		check = 0
	case 10:
		return 0, ErrInvalid
	}
	if int(s[10]-'0') != check {
		return 0, ErrInvalid
	}
	return strconv.ParseInt(s, 10, 64)
}

// Valid reports whether n is a well formed CUIT.
func Valid(n int64) bool {
	if n < 10_000_000_000 || n > 99_999_999_999 {
		return false
	}
	_, err := Parse(strconv.FormatInt(n, 10))
	return err == nil
}
