// Package awsmw provides AWS SDK v2 middleware for awsbnkctl.
//
// The SSO sentinel pattern is ported from aws-gpu-setup's lib/lab-core.sh
// `aws_q` + `check_auth_or_die` pattern. Instead of writing a tempfile and
// grepping stderr, the Go port uses an SDK Deserialize middleware that
// inspects the smithy APIError code and sets an atomic bool. Phase functions
// call CheckAuthOrDie at their start to hard-exit before more phases silently
// no-op.
//
// See docs/ARCHITECTURE.md for the full design.
package awsmw

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
)

// authFail is set to true by WithSSOWatch when an auth-class error is
// detected. Phase code reads it via CheckAuthOrDie.
var authFail atomic.Bool

// ExitFunc is the function called by CheckAuthOrDie when auth has failed.
// Defaults to os.Exit(99). Tests override this to capture the exit without
// terminating the test process.
var ExitFunc = func(code int) { os.Exit(code) }

// ResetForTest resets the auth-fail flag and restores the default ExitFunc.
// Call this at the start of any test that exercises CheckAuthOrDie.
func ResetForTest() {
	authFail.Store(false)
	ExitFunc = func(code int) { os.Exit(code) }
}

// WithSSOWatch returns an AWS SDK middleware option that, when applied to a
// client via config.WithAPIOptions, wraps every API call. On
// ExpiredToken-class errors it sets the authFail flag and lets the error
// propagate normally. Phase code then calls CheckAuthOrDie() to abort before
// the next phase.
func WithSSOWatch(stack *middleware.Stack) error {
	return stack.Deserialize.Add(
		middleware.DeserializeMiddlewareFunc("awsbnkctl.SSOWatch",
			func(ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler) (
				middleware.DeserializeOutput, middleware.Metadata, error,
			) {
				out, md, err := next.HandleDeserialize(ctx, in)
				if err != nil && isAuthError(err) {
					authFail.Store(true)
				}
				return out, md, err
			}),
		middleware.After,
	)
}

// isAuthError reports whether err is an AWS auth / credential expiry error.
func isAuthError(err error) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.ErrorCode() {
	case "ExpiredToken", "ExpiredTokenException",
		"InvalidClientTokenId", "UnauthorizedException",
		"InvalidIdentityToken":
		return true
	}
	// SSO-session-expired errors surface as a generic message rather than a
	// specific code on some SDK versions — scan the message too.
	if strings.Contains(ae.ErrorMessage(), "SSO session") ||
		strings.Contains(ae.ErrorMessage(), "Token has expired") {
		return true
	}
	return false
}

// CheckAuthOrDie hard-exits if an auth failure has been detected by the
// middleware. Call at the top of every phase function and at the entry to
// up/down so a mid-run SSO expiry produces a clear message rather than a
// cascade of confusing SDK errors.
//
// profile is the AWS profile name shown in the sso-login hint.
func CheckAuthOrDie(profile string) {
	if !authFail.Load() {
		return
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "FATAL: AWS auth failure detected mid-run — refusing to continue.")
	fmt.Fprintln(os.Stderr, "  Remaining phases would silently no-op and produce a false 'DONE' exit.")
	fmt.Fprintln(os.Stderr, "  Re-authenticate, then re-run:")
	fmt.Fprintf(os.Stderr, "    aws sso login --profile %s\n", profile)
	ExitFunc(99)
}

// AuthFailed reports the current state of the auth-fail sentinel without
// triggering an exit. Used by tests to assert the flag was set.
func AuthFailed() bool { return authFail.Load() }
