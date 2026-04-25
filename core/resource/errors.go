package resource

import "errors"

// ErrRollbackUnsupported is returned by Resource.RestoreVersion when the
// implementation cannot support the requested rollback (e.g. external-sql
// asked to restore a version whose staging table was GC'd).
var ErrRollbackUnsupported = errors.New("rollback unsupported by resource implementation")
