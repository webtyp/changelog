package changelog

type logError string

func (e logError) Error() string { return string(e) }

const (
	errConnRequired        logError = "changelog: conn is required"
	errAtLeastOneModel     logError = "changelog: at least one tracked model is required"
	errFactoryReturnedNil  logError = "changelog: tracked model factory returned nil"
	errTrackedTwice        logError = "changelog: model %s tracked twice"
	errNeedsCallerMintedPK logError = "changelog: tracked model %s needs a caller-minted primary key"
	errChangeLogMissing    logError = "changelog: change_log table missing, run changelog/migrate first: %v"
	errCreateNoPKValue     logError = "changelog: create on %s has no primary key value"
	errLimitPositive       logError = "changelog: limit must be positive"
)
