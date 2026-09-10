package applier

import (
	"github.com/noamsto/agent-smith/internal/analyst"
)

const prPlaceholder = analyst.PRPlaceholder

// AppendPRLink fills the applier placeholder in the reason-log entry whose first
// heading is "# <id>" with the PR URL.
func AppendPRLink(dir, id, prURL string) error {
	return analyst.LinkReasonLog(dir, id, "PR", prURL)
}
