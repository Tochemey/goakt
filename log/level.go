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
	InvalidLevel
)

// String returns the string representation of the level
func (l Level) String() string {
	switch l {
	case InfoLevel:
		return "info"
	case DebugLevel:
		return "debug"
	case WarningLevel:
		return "warn"
	case FatalLevel:
		return "fatal"
	case PanicLevel:
		return "panic"
	case ErrorLevel:
		return "error"
	default:
		return "" // FIXME: Surely we should blast here
	}
}
