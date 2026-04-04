// Package sftpfs implements rawfs.RawFS for SFTP (SSH File Transfer Protocol) servers.
package sftpfs

import (
	"fmt"
	"net"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/rakunlabs/pika/internal/rawfs"
)

func init() {
	rawfs.NewSFTPFSFunc = New
}

// FS implements rawfs.RawFS for SFTP servers.
type FS struct {
	host       string
	username   string
	password   string
	privateKey string
	basePath   string

	mu     sync.Mutex
	client *sftp.Client
	sshc   *ssh.Client
}

// New creates a new SFTP filesystem backend.
func New(host, username, password, privateKey, basePath string) (rawfs.RawFS, error) {
	if host == "" {
		return nil, fmt.Errorf("sftp: host is required")
	}

	if !strings.Contains(host, ":") {
		host += ":22"
	}

	basePath = strings.TrimSuffix(basePath, "/")

	fs := &FS{
		host:       host,
		username:   username,
		password:   password,
		privateKey: privateKey,
		basePath:   basePath,
	}

	// Test connection
	client, sshc, err := fs.connect()
	if err != nil {
		return nil, fmt.Errorf("sftp: initial connection failed: %w", err)
	}
	fs.client = client
	fs.sshc = sshc

	return fs, nil
}

func (f *FS) connect() (*sftp.Client, *ssh.Client, error) {
	var authMethods []ssh.AuthMethod

	if f.privateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(f.privateKey))
		if err != nil {
			return nil, nil, fmt.Errorf("parsing private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if f.password != "" {
		authMethods = append(authMethods, ssh.Password(f.password))
	}

	if len(authMethods) == 0 {
		authMethods = append(authMethods, ssh.Password(""))
	}

	config := &ssh.ClientConfig{
		User:            f.username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
	}

	sshConn, err := ssh.Dial("tcp", f.host, config)
	if err != nil {
		return nil, nil, fmt.Errorf("ssh dial: %w", err)
	}

	client, err := sftp.NewClient(sshConn)
	if err != nil {
		sshConn.Close()
		return nil, nil, fmt.Errorf("sftp client: %w", err)
	}

	return client, sshConn, nil
}

func (f *FS) getClient() (*sftp.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.client != nil {
		// Test with a stat on the root
		if _, err := f.client.Stat("/"); err == nil {
			return f.client, nil
		}
		f.client.Close()
		f.sshc.Close()
		f.client = nil
		f.sshc = nil
	}

	client, sshc, err := f.connect()
	if err != nil {
		return nil, err
	}

	f.client = client
	f.sshc = sshc
	return client, nil
}

func (f *FS) fullPath(relPath string) string {
	relPath = strings.Trim(relPath, "/")
	if f.basePath == "" {
		if relPath == "" {
			return "/"
		}
		return "/" + relPath
	}
	if relPath == "" {
		return f.basePath
	}
	return f.basePath + "/" + relPath
}

func (f *FS) Stat(relPath string) (*rawfs.FileInfo, error) {
	client, err := f.getClient()
	if err != nil {
		return nil, fmt.Errorf("sftp: %w", err)
	}

	fullPath := f.fullPath(relPath)
	info, err := client.Stat(fullPath)
	if err != nil {
		if isNotExist(err) {
			return nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
		}
		return nil, fmt.Errorf("sftp stat: %w", err)
	}

	return &rawfs.FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime(),
	}, nil
}

func (f *FS) ReadDir(relPath string) ([]rawfs.DirEntry, error) {
	client, err := f.getClient()
	if err != nil {
		return nil, fmt.Errorf("sftp: %w", err)
	}

	fullPath := f.fullPath(relPath)
	entries, err := client.ReadDir(fullPath)
	if err != nil {
		if isNotExist(err) {
			return nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
		}
		return nil, fmt.Errorf("sftp readdir: %w", err)
	}

	result := make([]rawfs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, rawfs.DirEntry{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Size:  entry.Size(),
		})
	}

	return result, nil
}

func (f *FS) Open(relPath string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	client, err := f.getClient()
	if err != nil {
		return nil, nil, fmt.Errorf("sftp: %w", err)
	}

	fullPath := f.fullPath(relPath)
	file, err := client.Open(fullPath)
	if err != nil {
		if isNotExist(err) {
			return nil, nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
		}
		return nil, nil, fmt.Errorf("sftp open: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("sftp stat after open: %w", err)
	}

	fi := &rawfs.FileInfo{
		Name:    path.Base(fullPath),
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime(),
	}

	// sftp.File implements io.ReadSeekCloser natively
	return file, fi, nil
}

func isNotExist(err error) bool {
	if os.IsNotExist(err) {
		return true
	}
	// sftp may return a *ssh.StatusError or a net error
	if _, ok := err.(*net.OpError); ok {
		return false // network error, not "not found"
	}
	// Check error message for common SFTP "no such file" patterns
	if strings.Contains(err.Error(), "not exist") || strings.Contains(err.Error(), "no such file") {
		return true
	}
	return false
}

// Rename renames a file or directory on the SFTP server.
func (f *FS) Rename(oldPath, newPath string) error {
	client, err := f.getClient()
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}

	oldFull := f.fullPath(oldPath)
	newFull := f.fullPath(newPath)

	if err := client.Rename(oldFull, newFull); err != nil {
		return fmt.Errorf("sftp rename: %w", err)
	}
	return nil
}

var _ rawfs.RawFS = (*FS)(nil)
var _ rawfs.RenamableRawFS = (*FS)(nil)
