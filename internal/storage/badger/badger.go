package badger

import "github.com/dgraph-io/badger/v4"

var (
	DefaultCacheSize int64 = 100 << 20 // 100 MB
	DefaultLogSize   int64 = 100 << 20 // 100 MB
)

type Badger struct {
	db *badger.DB
}

func New(opts ...Option) (*Badger, error) {
	o := option{
		Flatten: true,
		Memory:  false,
		Logger:  NewLogger(),
	}

	for _, opt := range opts {
		opt(&o)
	}

	badgerOpts := badger.DefaultOptions(o.Path).
		WithValueLogFileSize(DefaultLogSize).
		WithIndexCacheSize(DefaultCacheSize).
		WithReadOnly(o.ReadOnly).
		WithLogger(o.Logger)

	db, err := badger.Open(badgerOpts)
	if err != nil {
		return nil, err
	}

	if o.Flatten {
		if err := db.Flatten(20); err != nil {
			return nil, err
		}
	}

	return &Badger{
		db: db,
	}, nil
}

// ////////////////////////////////

type option struct {
	Path       string
	Memory     bool
	BackupPath string
	Flatten    bool
	ReadOnly   bool
	Logger     *Logger
}

type Option func(*option)

func WithMemory(memory bool) Option {
	return func(o *option) {
		o.Memory = memory
	}
}

func WithBackupPath(backupPath string) Option {
	return func(o *option) {
		o.BackupPath = backupPath
	}
}

func WithFlatten(flatten bool) Option {
	return func(o *option) {
		o.Flatten = flatten
	}
}

func WithReadOnly(readOnly bool) Option {
	return func(o *option) {
		o.ReadOnly = readOnly
	}
}
