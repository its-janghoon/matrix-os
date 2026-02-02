package admin

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LogsService handles log retrieval and streaming
type LogsService struct {
	logs   []LogEntry
	logsMu sync.RWMutex
	maxLogs int
}

// LogEntry represents a log entry
type LogEntry struct {
	Timestamp time.Time
	Level     string // "debug", "info", "warn", "error"
	Component string // "agent", "matrix", "p2p", "soul", etc.
	Message   string
	Fields    map[string]interface{}
}

// NewLogsService creates a new logs service
func NewLogsService() *LogsService {
	return &LogsService{
		logs:    make([]LogEntry, 0),
		maxLogs: 10000, // Keep last 10k logs
	}
}

// AddLog adds a new log entry
func (s *LogsService) AddLog(level, component, message string, fields map[string]interface{}) {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()

	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Component: component,
		Message:   message,
		Fields:    fields,
	}

	s.logs = append(s.logs, entry)

	// Trim logs if we exceed maxLogs
	if len(s.logs) > s.maxLogs {
		s.logs = s.logs[len(s.logs)-s.maxLogs:]
	}
}

// GetLogs retrieves logs matching the given filters
func (s *LogsService) GetLogs(ctx context.Context, filters LogFilters) ([]LogEntry, error) {
	s.logsMu.RLock()
	defer s.logsMu.RUnlock()

	var result []LogEntry

	for _, entry := range s.logs {
		// Apply filters
		if filters.Level != "" && entry.Level != filters.Level {
			continue
		}
		if filters.Component != "" && entry.Component != filters.Component {
			continue
		}
		if !filters.Since.IsZero() && entry.Timestamp.Before(filters.Since) {
			continue
		}
		if !filters.Until.IsZero() && entry.Timestamp.After(filters.Until) {
			continue
		}

		result = append(result, entry)
	}

	// Apply limit
	if filters.Limit > 0 && len(result) > filters.Limit {
		result = result[len(result)-filters.Limit:]
	}

	return result, nil
}

// StreamLogs streams logs matching the given filters
func (s *LogsService) StreamLogs(ctx context.Context, filters LogFilters, ch chan<- LogEntry) error {
	defer close(ch)

	// Get initial logs
	logs, err := s.GetLogs(ctx, filters)
	if err != nil {
		return fmt.Errorf("failed to get initial logs: %w", err)
	}

	// Send initial logs
	for _, entry := range logs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- entry:
		}
	}

	// Stream new logs
	lastIndex := len(s.logs)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			s.logsMu.RLock()
			if len(s.logs) > lastIndex {
				for i := lastIndex; i < len(s.logs); i++ {
					entry := s.logs[i]
					// Apply filters
					if filters.Level != "" && entry.Level != filters.Level {
						continue
					}
					if filters.Component != "" && entry.Component != filters.Component {
						continue
					}

					select {
					case <-ctx.Done():
						s.logsMu.RUnlock()
						return ctx.Err()
					case ch <- entry:
					}
				}
				lastIndex = len(s.logs)
			}
			s.logsMu.RUnlock()

			// Small sleep to avoid busy waiting
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// LogFilters represents filters for log queries
type LogFilters struct {
	Level     string
	Component string
	Since     time.Time
	Until     time.Time
	Limit     int
}
