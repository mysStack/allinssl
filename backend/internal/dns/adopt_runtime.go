package dns

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"ALLinSSL/backend/internal/dnscontrol"
)

const (
	productionDNSControlPath  = "/usr/local/bin/dnscontrol"
	developmentDNSControlPath = "/home/bruce/.local/bin/dnscontrol-5.0.3"
)

var dnsControlWorkRoot = filepath.Join("data", "dnscontrol", "jobs")

func NewSQLAdoptService(database *sql.DB) (*AdoptService, error) {
	store := NewStore(database)
	if err := store.EnsureSchema(context.Background()); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dnsControlWorkRoot, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dnsControlWorkRoot, 0o700); err != nil {
		return nil, err
	}
	engine, err := dnscontrol.New(dnscontrol.Config{
		BinaryPath: defaultDNSControlPath(),
		WorkRoot:   dnsControlWorkRoot,
	}, nil)
	if err != nil {
		return nil, err
	}
	reader := NewSQLService(database)
	return NewAdoptService(reader, reader.credentials, store, engine), nil
}

func defaultDNSControlPath() string {
	if info, err := os.Stat(productionDNSControlPath); err == nil && info.Mode().IsRegular() {
		return productionDNSControlPath
	}
	if info, err := os.Stat(developmentDNSControlPath); err == nil && info.Mode().IsRegular() {
		return developmentDNSControlPath
	}
	return productionDNSControlPath
}
