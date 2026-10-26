package update

import (
	"fmt"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// RulesError says the new version can't build some of the user's enabled rules. The job
// carries the rules, so a client can show them one by one instead of parsing a message.
type RulesError struct{ Problems []*pb.RuleProblem }

func (e *RulesError) Error() string {
	return fmt.Sprintf("the new version can't build %d of your rules", len(e.Problems))
}
