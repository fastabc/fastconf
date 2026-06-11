package fastconf

import "github.com/fastabc/fastconf/internal/fcerr"

var ErrFastConf = fcerr.ErrFastConf

var (
	ErrNoSources  = fcerr.ErrNoSources
	ErrValidation = fcerr.ErrValidation
	ErrDecode     = fcerr.ErrDecode
	ErrMerge      = fcerr.ErrMerge
	ErrPatch      = fcerr.ErrPatch
	ErrClosed     = fcerr.ErrClosed
	ErrValidator  = fcerr.ErrValidator
	ErrTransform  = fcerr.ErrTransform
	ErrProvider   = fcerr.ErrProvider
	ErrGenerator  = fcerr.ErrGenerator
	ErrNoOrigin   = fcerr.ErrNoOrigin
)

type ReloadError = fcerr.ReloadError

var ErrPolicyDenied = fcerr.ErrPolicyDenied

type PolicyError = fcerr.PolicyError
