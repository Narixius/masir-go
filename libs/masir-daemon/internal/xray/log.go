package xray

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	applog "github.com/xtls/xray-core/app/log"
	clog "github.com/xtls/xray-core/common/log"
)

// LogSink receives Xray's own warnings and errors, so `masir logs` can show them.
// The main Xray instance writes its error log to a file; temporary latency-test
// instances keep the console logger, so they don't flood the daemon's log.
var (
	logSinkMu sync.RWMutex
	logSink   func(level, msg string)
)

// SetLogSink installs the sink and registers the Xray file handler that forwards to it.
// Call it before Xray starts.
func SetLogSink(sink func(level, msg string)) {
	logSinkMu.Lock()
	logSink = sink
	logSinkMu.Unlock()
	_ = applog.RegisterHandlerCreator(applog.LogType_File, fileLogHandler)
}

func currentLogSink() func(level, msg string) {
	logSinkMu.RLock()
	defer logSinkMu.RUnlock()
	return logSink
}

func fileLogHandler(_ applog.LogType, options applog.HandlerCreatorOptions) (clog.Handler, error) {
	f, err := os.OpenFile(options.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &teeHandler{
		file:   f,
		logger: log.New(f, "", log.Ldate|log.Ltime|log.Lmicroseconds),
		sink:   currentLogSink(),
	}, nil
}

// teeHandler keeps Xray's error log file and also feeds the daemon's log stream.
// It writes the file directly rather than through Xray's background logger, so Close
// actually releases the file (important on Windows, where an open log file blocks
// temp-directory cleanup and reinstalls).
type teeHandler struct {
	mu     sync.Mutex
	file   *os.File
	logger *log.Logger
	sink   func(level, msg string)
}

// Close closes the file synchronously when Xray shuts down.
func (h *teeHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file == nil {
		return nil
	}
	err := h.file.Close()
	h.file, h.logger = nil, nil
	return err
}

func (h *teeHandler) Handle(msg clog.Message) {
	h.mu.Lock()
	if h.logger != nil {
		h.logger.Print(msg.String())
	}
	h.mu.Unlock()

	if h.sink == nil {
		return
	}
	gm, ok := msg.(*clog.GeneralMessage)
	if !ok {
		return
	}
	text := strings.TrimSpace(fmt.Sprint(gm.Content))
	if text == "" {
		return
	}
	h.sink(severityLevel(gm.Severity), text)
}

func severityLevel(s clog.Severity) string {
	switch s {
	case clog.Severity_Error:
		return "error"
	case clog.Severity_Warning:
		return "warn"
	case clog.Severity_Debug:
		return "debug"
	default:
		return "info"
	}
}
