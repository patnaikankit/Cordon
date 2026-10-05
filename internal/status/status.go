package status

import "errors"

// Standard status and exit codes for Cordon tool executions.
const (
	StatusOK           = 0
	StatusError        = 1
	StatusTimeout      = 124 // Conforms to GNU timeout standard
	StatusPolicyDenied = 126 // Permission denied / policy blocked execution
	StatusNotFound      = 127 // Command or tool not found
	StatusCanceled      = 130 // 128 + SIGINT (2), standard shell exit code for cancellation
	StatusLimitExceeded = 1   // Resource or count limit exceeded
)

// Internal sentinel errors for Cordon execution and dispatch.
var (
	ErrPolicyDenied    = errors.New("cordon: policy denied execution")
	ErrUnknownTool     = errors.New("cordon: unknown tool")
	ErrMalformedInput  = errors.New("cordon: malformed tool input")
	ErrCommandNotFound = errors.New("cordon: command not found")
	ErrCommandLimit    = errors.New("cordon: command count limit exceeded")
	ErrOutputLimit     = errors.New("cordon: output byte limit exceeded")
	ErrInputLimit      = errors.New("cordon: input byte limit exceeded")
	ErrCallClosed      = errors.New("cordon: call terminated")
	ErrTimeout         = errors.New("cordon: execution timed out")
	ErrCanceled        = errors.New("cordon: execution canceled")
	ErrPanic           = errors.New("cordon: execution panicked")
)
