package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

var (
	// log is the global logger instance
	log zerolog.Logger

	// DefaultLevel is the default logging level
	DefaultLevel = "info"

	// levels maps string level names to zerolog levels
	levels = map[string]zerolog.Level{
		"debug":    zerolog.DebugLevel,
		"info":     zerolog.InfoLevel,
		"warn":     zerolog.WarnLevel,
		"error":    zerolog.ErrorLevel,
		"fatal":    zerolog.FatalLevel,
		"panic":    zerolog.PanicLevel,
		"disabled": zerolog.Disabled,
	}
)

// init initializes the global logger with default settings
func init() {
	// Default to console writer
	output := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
		FormatLevel: func(i interface{}) string {
			if ll, ok := i.(string); ok {
				return strings.ToUpper(ll)
			}
			return "???"
		},
	}

	// Initialize the global logger
	initLogger(output, DefaultLevel)
}

// initLogger sets up the global logger with the given writer and level
func initLogger(output io.Writer, levelStr string) {
	level, exists := levels[strings.ToLower(levelStr)]
	if !exists {
		level = zerolog.InfoLevel
		fmt.Fprintf(os.Stderr, "Unknown log level '%s', defaulting to 'info'\n", levelStr)
	}

	// Configure the global logger
	zerolog.SetGlobalLevel(level)
	zerolog.TimestampFieldName = "time"
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "msg"

	log = zerolog.New(output).With().Timestamp().Logger()
}

// SetLevel changes the logging level
func SetLevel(levelStr string) {
	if level, exists := levels[strings.ToLower(levelStr)]; exists {
		zerolog.SetGlobalLevel(level)
	} else {
		fmt.Fprintf(os.Stderr, "Unknown log level '%s', leaving at current level\n", levelStr)
	}
}

// Debug logs a debug message with optional key-value pairs
func Debug(msg string, keysAndValues ...interface{}) {
	event := log.Debug()
	logEvent(event, msg, keysAndValues...)
}

// Info logs an info message with optional key-value pairs
func Info(msg string, keysAndValues ...interface{}) {
	event := log.Info()
	logEvent(event, msg, keysAndValues...)
}

// Warn logs a warning message with optional key-value pairs
func Warn(msg string, keysAndValues ...interface{}) {
	event := log.Warn()
	logEvent(event, msg, keysAndValues...)
}

// Error logs an error message with optional key-value pairs
func Error(msg string, keysAndValues ...interface{}) {
	event := log.Error()
	logEvent(event, msg, keysAndValues...)
}

// Fatal logs a fatal message with optional key-value pairs and then exits
func Fatal(msg string, keysAndValues ...interface{}) {
	event := log.Fatal()
	logEvent(event, msg, keysAndValues...)
}

// logEvent adds key-value pairs to the event and sends it
func logEvent(event *zerolog.Event, msg string, keysAndValues ...interface{}) {
	// Process key-value pairs
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			key, ok := keysAndValues[i].(string)
			if !ok {
				key = fmt.Sprintf("%v", keysAndValues[i])
			}

			// Handle error type specially to ensure proper formatting
			if err, ok := keysAndValues[i+1].(error); ok {
				event = event.AnErr(key, err)
			} else {
				// For other types, use Interface method
				event = event.Interface(key, keysAndValues[i+1])
			}
		} else {
			// Odd number of arguments, add the last one as an orphaned value
			event = event.Interface("orphaned", keysAndValues[i])
		}
	}

	event.Msg(msg)
}
