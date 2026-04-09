package rawfs

// This file contains the pluggable factory functions for S3 and FTP backends.
// Each backend package registers itself by setting these variables during initialization.

// NewS3FSFunc is the factory function for creating S3 backends.
// Set by the s3fs package.
var NewS3FSFunc func(bucket, region, endpoint, accessKey, secretKey, prefix string, pathStyle bool, secure *bool) (RawFS, error)

// NewFTPFSFunc is the factory function for creating FTP backends.
// Set by the ftpfs package.
var NewFTPFSFunc func(host, username, password, basePath string, tls bool) (RawFS, error)

// NewSFTPFSFunc is the factory function for creating SFTP backends.
// Set by the sftpfs package.
var NewSFTPFSFunc func(host, username, password, privateKey, basePath string) (RawFS, error)

// NewWebDAVFSFunc is the factory function for creating WebDAV backends.
// Set by the webdavfs package.
var NewWebDAVFSFunc func(url, username, password, basePath string) (RawFS, error)

// NewVercelBlobFSFunc is the factory function for creating Vercel Blob backends.
// Set by the vercelblobfs package.
var NewVercelBlobFSFunc func(token, storeID, prefix string) (RawFS, error)
