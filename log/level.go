package log

// Level specifies the log level
type Level int

const (
	// InfoLevel indicates Info log level.
	InfoLevel Level = iota
	// WarningLevel indicates Warning log level.
	WarningLevel
	// ErrorLevel indicates Error log level.
	ErrorLevel
	// FatalLevel indicates Fatal log level.
	FatalLevel
	// PanicLevel indicates Panic log level
	PanicLevel
	// DebugLevel indicates Debug log level
	DebugLevel
	Disabled
	numLogLevels = 6
)

// levels is internally used to provide the default logger
var levels = [numLogLevels]string{
	InfoLevel:    "INFO",
	WarningLevel: "WARNING",
	ErrorLevel:   "ERROR",
	FatalLevel:   "FATAL",
	PanicLevel:   "PANIC",
	DebugLevel:   "DEBUG",
}
