package ledger

import (
	"fmt"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// BalanceError says where a statement stops closing.
type BalanceError struct {
	Line     int       // 0 when the mismatch is on the totals
	Sheet    int       // workbook sheet of Line, 0 for a text file
	Date     time.Time // date of the movement on Line
	Check    string
	Expected money.Amount
	Got      money.Amount
}

func (e *BalanceError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s at %s (%s): expected %s, file says %s, a difference of %s",
			e.Check, where(e.Sheet, e.Line), day(e.Date), e.Expected, e.Got, e.Got-e.Expected)
	}
	return fmt.Sprintf("%s: expected %s, file says %s, a difference of %s", e.Check, e.Expected, e.Got, e.Got-e.Expected)
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
				return &BalanceError{Line: m.Line, Sheet: m.Sheet, Date: m.Date, Check: "running balance", Expected: want, Got: *m.Balance}
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
