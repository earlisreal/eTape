//go:build wails && !server

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/earlisreal/eTape/engine/internal/config"
	"github.com/earlisreal/eTape/engine/internal/profile"
	"github.com/earlisreal/eTape/engine/internal/singleinstance"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type wailsInstance struct {
	options *application.SingleInstanceOptions
	release func() error
}

// prepareWailsInstance acquires the eTape data lock before application.New.
// Wails' mutex then provides activation for the already-running native host;
// the database lock remains the storage authority.
func prepareWailsInstance(onSecond func()) (wailsInstance, error) {
	fs := flag.NewFlagSet("etape-wails", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "")
	profileKind := fs.String("profile", os.Getenv("ETAPE_PROFILE"), "")
	dataRoot := fs.String("data-root", os.Getenv("ETAPE_DATA_ROOT"), "")
	allowReal := fs.Bool("allow-real-profile", envBool("ETAPE_ALLOW_REAL_PROFILE"), "")
	demo := fs.Bool("demo", false, "")
	logPath := fs.String("log", "", "")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return wailsInstance{}, fmt.Errorf("parse Wails launch flags: %w", err)
	}

	kind := profile.Kind(*profileKind)
	if *demo {
		kind = profile.KindDemo
	}
	paths, err := profile.Resolve(profile.Request{
		Kind: kind, Root: *dataRoot, ConfigPath: *configPath, LogPath: *logPath, AllowReal: *allowReal,
	})
	if err != nil {
		return wailsInstance{}, err
	}
	cfg, err := config.Load(paths.ConfigPath)
	if err != nil {
		return wailsInstance{}, err
	}
	dbPath := cfg.Store.DBPath
	if dbPath == "" {
		dbPath = paths.DBPath
	} else if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(filepath.Dir(paths.ConfigPath), dbPath)
	}
	if err := paths.ValidateDataPath(dbPath); err != nil {
		return wailsInstance{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return wailsInstance{}, err
	}

	release, err := singleinstance.Acquire(dbPath + ".lock")
	if err != nil && !errors.Is(err, singleinstance.ErrAlreadyRunning) {
		return wailsInstance{}, err
	}
	if errors.Is(err, singleinstance.ErrAlreadyRunning) {
		release = nil
	}

	return wailsInstance{
		options: &application.SingleInstanceOptions{
			UniqueID:               wailsInstanceID(dbPath),
			ExitCode:               0,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { onSecond() },
		},
		release: release,
	}, nil
}

func wailsInstanceID(dbPath string) string {
	canonical, err := filepath.Abs(filepath.Clean(dbPath))
	if err != nil {
		canonical = filepath.Clean(dbPath)
	}
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	sum := sha256.Sum256([]byte(canonical))
	return "com.earlisreal.etape." + hex.EncodeToString(sum[:])
}
