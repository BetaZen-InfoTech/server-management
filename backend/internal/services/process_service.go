package services

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
)

type ProcessService struct {
	db *mongo.Database
}

func NewProcessService(db *mongo.Database) *ProcessService {
	return &ProcessService{db: db}
}

// List returns a list of running processes sorted by the given field with a limit.
func (s *ProcessService) List(ctx context.Context, sort string, limit int) ([]map[string]interface{}, error) {
	// TODO: implement - read /proc or use ps, sort by cpu/memory, return top N
	return nil, nil
}

// GetByPID returns detailed information about a specific process.
func (s *ProcessService) GetByPID(ctx context.Context, pid string) (map[string]interface{}, error) {
	// TODO: implement - read /proc/<pid>/status, stat, cmdline
	return nil, nil
}

// Kill sends a signal to a process.
func (s *ProcessService) Kill(ctx context.Context, pid string, signal string) error {
	// TODO: implement - send specified signal to PID
	return nil
}

// ListServices returns the status of all managed systemd services.
func (s *ProcessService) ListServices(ctx context.Context) ([]map[string]interface{}, error) {
	// TODO: implement - query systemctl list-units for relevant services
	return nil, nil
}

// ControlService performs an action (start, stop, restart, enable, disable) on a service.
func (s *ProcessService) ControlService(ctx context.Context, name string, action string) error {
	// TODO: implement - run systemctl <action> <name>
	return nil
}
