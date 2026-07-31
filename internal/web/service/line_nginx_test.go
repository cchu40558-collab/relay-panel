package service

import (
	"errors"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type fakeNginxExecutor struct {
	files    map[string][]byte
	outputs  map[string]string
	failures map[string]error
	commands []string
	now      int64
}

func newFakeNginxExecutor() *fakeNginxExecutor {
	return &fakeNginxExecutor{
		files:    map[string][]byte{},
		outputs:  map[string]string{},
		failures: map[string]error{},
		now:      1700000000,
	}
}

func (f *fakeNginxExecutor) GOOS() string {
	return "linux"
}

func (f *fakeNginxExecutor) ReadFile(path string) ([]byte, error) {
	body, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), body...), nil
}

func (f *fakeNginxExecutor) WriteFile(path string, body []byte, _ os.FileMode) error {
	f.files[path] = append([]byte(nil), body...)
	return nil
}

func (f *fakeNginxExecutor) MkdirAll(_ string, _ os.FileMode) error {
	return nil
}

func (f *fakeNginxExecutor) Remove(path string) error {
	if _, ok := f.files[path]; !ok {
		return fs.ErrNotExist
	}
	delete(f.files, path)
	return nil
}

func (f *fakeNginxExecutor) RunCommand(name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.commands = append(f.commands, key)
	return f.outputs[key], f.failures[key]
}

func (f *fakeNginxExecutor) NowUnix() int64 {
	return f.now
}

func TestApplyCloudflareNginxWithExecutorSuccessBacksUpAndReloads(t *testing.T) {
	executor := newFakeNginxExecutor()
	path := "/etc/nginx/conf.d/x-ui-line-1.conf"
	backupPath := path + ".bak.1700000000"
	executor.files[path] = []byte("old config")

	line, config := testCloudflareNginxLine(path)
	detail, err := applyCloudflareNginxWithExecutor(line, config, executor)
	if err != nil {
		t.Fatalf("applyCloudflareNginxWithExecutor error: %v", err)
	}
	if !strings.Contains(detail, "backup="+backupPath) {
		t.Fatalf("detail missing backup path: %s", detail)
	}
	if string(executor.files[backupPath]) != "old config" {
		t.Fatalf("backup = %q", string(executor.files[backupPath]))
	}
	if got := string(executor.files[path]); !strings.Contains(got, "proxy_pass http://127.0.0.1:30001;") {
		t.Fatalf("new config not written: %s", got)
	}
	if got := string(executor.files[path]); !strings.Contains(got, "ssl_certificate /etc/nginx/ssl/origin.crt;") || !strings.Contains(got, "ssl_certificate_key /etc/nginx/ssl/origin.key;") {
		t.Fatalf("TLS paths not written: %s", got)
	}
	wantCommands := []string{"nginx -t", "systemctl reload nginx"}
	if !reflect.DeepEqual(executor.commands, wantCommands) {
		t.Fatalf("commands = %+v, want %+v", executor.commands, wantCommands)
	}
}

func TestApplyCloudflareNginxWithExecutorNginxTestFailureRollsBackExistingConfig(t *testing.T) {
	executor := newFakeNginxExecutor()
	path := "/etc/nginx/conf.d/x-ui-line-1.conf"
	executor.files[path] = []byte("old config")
	executor.failures["nginx -t"] = errors.New("syntax error")
	executor.outputs["nginx -t"] = "nginx: configuration file test failed"

	line, config := testCloudflareNginxLine(path)
	_, err := applyCloudflareNginxWithExecutor(line, config, executor)
	if err == nil || !strings.Contains(err.Error(), "rolled back nginx config") {
		t.Fatalf("error = %v, want rollback error", err)
	}
	if string(executor.files[path]) != "old config" {
		t.Fatalf("config not restored: %q", string(executor.files[path]))
	}
	wantCommands := []string{"nginx -t"}
	if !reflect.DeepEqual(executor.commands, wantCommands) {
		t.Fatalf("commands = %+v, want %+v", executor.commands, wantCommands)
	}
}

func TestApplyCloudflareNginxWithExecutorReloadFailureRollsBackExistingConfig(t *testing.T) {
	executor := newFakeNginxExecutor()
	path := "/etc/nginx/conf.d/x-ui-line-1.conf"
	executor.files[path] = []byte("old config")
	executor.failures["systemctl reload nginx"] = errors.New("systemctl failed")
	executor.outputs["systemctl reload nginx"] = "unit reload failed"
	executor.failures["service nginx reload"] = errors.New("service failed")
	executor.outputs["service nginx reload"] = "service reload failed"

	line, config := testCloudflareNginxLine(path)
	_, err := applyCloudflareNginxWithExecutor(line, config, executor)
	if err == nil || !strings.Contains(err.Error(), "rolled back nginx config") {
		t.Fatalf("error = %v, want rollback error", err)
	}
	if string(executor.files[path]) != "old config" {
		t.Fatalf("config not restored: %q", string(executor.files[path]))
	}
	wantCommands := []string{"nginx -t", "systemctl reload nginx", "service nginx reload"}
	if !reflect.DeepEqual(executor.commands, wantCommands) {
		t.Fatalf("commands = %+v, want %+v", executor.commands, wantCommands)
	}
}

func TestApplyCloudflareNginxWithExecutorNginxTestFailureRemovesNewConfig(t *testing.T) {
	executor := newFakeNginxExecutor()
	path := "/etc/nginx/conf.d/x-ui-line-1.conf"
	executor.failures["nginx -t"] = errors.New("syntax error")

	line, config := testCloudflareNginxLine(path)
	_, err := applyCloudflareNginxWithExecutor(line, config, executor)
	if err == nil || !strings.Contains(err.Error(), "rolled back nginx config") {
		t.Fatalf("error = %v, want rollback error", err)
	}
	if _, ok := executor.files[path]; ok {
		t.Fatalf("new config should be removed after rollback: %q", string(executor.files[path]))
	}
}

func TestApplyCloudflareNginxWithExecutorRequiresTLSPathsBeforeWrite(t *testing.T) {
	executor := newFakeNginxExecutor()
	path := "/etc/nginx/conf.d/x-ui-line-1.conf"
	line, config := testCloudflareNginxLine(path)
	delete(config, "nginxCertFile")

	_, err := applyCloudflareNginxWithExecutor(line, config, executor)
	if err == nil || !strings.Contains(err.Error(), "origin certificate path is required") {
		t.Fatalf("error = %v, want missing certificate path", err)
	}
	if len(executor.commands) != 0 {
		t.Fatalf("commands = %v, want no writes or Nginx commands", executor.commands)
	}
	if _, ok := executor.files[path]; ok {
		t.Fatalf("config must not be written without TLS paths")
	}
}

func testCloudflareNginxLine(path string) (model.LineProfile, map[string]string) {
	return model.LineProfile{
			Id:        1,
			Name:      "cf-main",
			Type:      LineTypeCloudflare,
			EntryHost: "proxy.example.com",
			EntryPort: 8443,
		}, map[string]string{
			"nginxApply":      "true",
			"nginxConfigPath": path,
			"nginxCertFile":   "/etc/nginx/ssl/origin.crt",
			"nginxKeyFile":    "/etc/nginx/ssl/origin.key",
			"wsPath":          "/ws",
			"localXrayPort":   "30001",
		}
}
