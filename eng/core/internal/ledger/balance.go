package ledger

import (
	"fmt"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// BalanceError says where a statement stops closing.
type BalanceError struct {
	Line     int // 0 when the mismatch is on the totals
	Check    string
	Expected money.Amount
	Got      money.Amount
}

func (e *BalanceError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s at line %d: expected %s, file says %s", e.Check, e.Line, e.Expected, e.Got)
	}
	return fmt.Sprintf("%s: expected %s, file says %s", e.Check, e.Expected, e.Got)
}

// CheckBalance verifies the statement before anything runs on it:
//
//   - opening + credits - debits = closing;
//   - when rows carry a running balance, each one equals the previous plus
//     the movement, starting from the opening;
//   - when the file states a closing balance and rows carry a running
//     balance, the last one equals the stated closing.
//
// A statement with neither stated balances nor a running balance has no
// evidence to check and is refused.
func CheckBalance(st *Statement) error {
	if !st.Running && !(st.OpeningStated && st.ClosingStated) {
		return &BalanceError{Check: "no balance evidence in the file"}
	}
	if st.Running {
		prev := st.Opening
		for _, m := range st.Movements {
			want := prev + m.Amount
			if *m.Balance != want {
				return &BalanceError{Line: m.Line, Check: "running balance", Expected: want, Got: *m.Balance}
			}
			prev = want
		}
	}
	want := st.Opening + st.Credits() - st.Debits()
	if want != st.Closing {
		return &BalanceError{Check: "opening + credits - debits = closing", Expected: want, Got: st.Closing}
	}
	return nil
}
