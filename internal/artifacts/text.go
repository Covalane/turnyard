package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"strings"
	"unicode/utf8"
)

func inspectText(spec contracts.DeliverableSpec, claim Claim) Result {
	result := Result{ID: spec.ID, Kind: spec.Kind, Status: StatusMissing, Verification: VerificationTextPresence,
		ExpectedSHA256: spec.ExpectedSHA256, ErrorCode: string(fault.CodeDeliverableMissing)}
	if claim.ID == "" || claim.Text == "" {
		return result
	}
	result.Text = claim.Text
	result.Bytes = int64(len(claim.Text))
	if !utf8.ValidString(claim.Text) || strings.TrimSpace(claim.Text) == "" || result.Bytes > 64<<10 || claim.URL != "" {
		result.Status, result.ErrorCode = StatusInvalid, string(fault.CodeDeliverableInvalid)
		return result
	}
	sum := sha256.Sum256([]byte(claim.Text))
	result.SHA256 = hex.EncodeToString(sum[:])
	result.Status, result.ErrorCode = StatusPresent, ""
	if spec.ExpectedSHA256 != "" && spec.ExpectedSHA256 != result.SHA256 {
		result.Status, result.ErrorCode = StatusMismatch, string(fault.CodeDeliverableMismatch)
	}
	return result
}
