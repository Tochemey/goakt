package log

// Logger represents an active logging object that generates lines of
// output to an io.Writer.
type Logger interface {
	// Info starts a new message with info level.
	Info(...any)
	// Infof starts a new message with info level.
	Infof(string, ...any)
	// Warn starts a new message with warn level.
	Warn(...any)
	// Warnf starts a new message with warn level.
	Warnf(string, ...any)
	// Error starts a new message with error level.
	Error(...any)
	// Errorf starts a new message with error level.
	Errorf(string, ...any)
	// Fatal starts a new message with fatal level. The os.Exit(1) function
	// is called which terminates the program immediately.
	Fatal(...any)
	// Fatalf starts a new message with fatal level. The os.Exit(1) function
	// is called which terminates the program immediately.
	Fatalf(string, ...any)
	// Panic starts a new message with panic level. The panic() function
	// is called which stops the ordinary flow of a goroutine.
	Panic(...any)
	// Panicf starts a new message with panic level. The panic() function
	// is called which stops the ordinary flow of a goroutine.
	Panicf(string, ...any)
	// Debug starts a new message with debug level.
	Debug(...any)
	// Debugf starts a new message with debug level.
	Debugf(string, ...any)
	// LogLevel returns the log level being used
	LogLevel() Level
}
