package config

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/siddontang/go-mysql/mysql"

	"github.com/pingcap/dm/pkg/binlog"
	"github.com/pingcap/dm/pkg/gtid"
	"github.com/pingcap/dm/pkg/log"
	"github.com/pingcap/dm/pkg/terror"
	"github.com/pingcap/dm/pkg/tracing"
	"github.com/pingcap/dm/pkg/utils"
)

const (
	// dbReadTimeout is readTimeout for DB connection in adjust
	dbReadTimeout = "30s"
	// dbGetTimeout is timeout for getting some information from DB
	dbGetTimeout = 30 * time.Second

	// the default base(min) server id generated by random
	defaultBaseServerID = math.MaxUint32 / 10
)

var getAllServerIDFunc = utils.GetAllServerID

// PurgeConfig is the configuration for Purger
type PurgeConfig struct {
	Interval    int64 `toml:"interval" json:"interval"`         // check whether need to purge at this @Interval (seconds)
	Expires     int64 `toml:"expires" json:"expires"`           // if file's modified time is older than @Expires (hours), then it can be purged
	RemainSpace int64 `toml:"remain-space" json:"remain-space"` // if remain space in @RelayBaseDir less than @RemainSpace (GB), then it can be purged
}

// SourceConfig is the configuration for Worker
type SourceConfig struct {
	EnableGTID  bool   `toml:"enable-gtid" json:"enable-gtid"`
	AutoFixGTID bool   `toml:"auto-fix-gtid" json:"auto-fix-gtid"`
	RelayDir    string `toml:"relay-dir" json:"relay-dir"`
	MetaDir     string `toml:"meta-dir" json:"meta-dir"`
	Flavor      string `toml:"flavor" json:"flavor"`
	Charset     string `toml:"charset" json:"charset"`

	EnableRelay bool `toml:"enable-relay" json:"enable-relay"`
	// relay synchronous starting point (if specified)
	RelayBinLogName string `toml:"relay-binlog-name" json:"relay-binlog-name"`
	RelayBinlogGTID string `toml:"relay-binlog-gtid" json:"relay-binlog-gtid"`

	SourceID string   `toml:"source-id" json:"source-id"`
	From     DBConfig `toml:"from" json:"from"`

	// config items for purger
	Purge PurgeConfig `toml:"purge" json:"purge"`

	// config items for task status checker
	Checker CheckerConfig `toml:"checker" json:"checker"`

	// config items for tracer
	Tracer tracing.Config `toml:"tracer" json:"tracer"`

	// id of the worker on which this task run
	ServerID uint32 `toml:"server-id" json:"server-id"`
}

// NewSourceConfig creates a new base config for upstream MySQL/MariaDB source.
func NewSourceConfig() *SourceConfig {
	c := &SourceConfig{
		RelayDir: "relay-dir",
		Purge: PurgeConfig{
			Interval:    60 * 60,
			Expires:     0,
			RemainSpace: 15,
		},
		Checker: CheckerConfig{
			CheckEnable:     true,
			BackoffRollback: &Duration{DefaultBackoffRollback},
			BackoffMax:      &Duration{DefaultBackoffMax},
		},
		Tracer: tracing.Config{
			Enable:     false,
			TracerAddr: "",
			BatchSize:  20,
			Checksum:   false,
		},
	}
	c.adjust()
	return c
}

// Clone clones a config
func (c *SourceConfig) Clone() *SourceConfig {
	clone := &SourceConfig{}
	*clone = *c
	return clone
}

// Toml returns TOML format representation of config
func (c *SourceConfig) Toml() (string, error) {
	var b bytes.Buffer

	err := toml.NewEncoder(&b).Encode(c)
	if err != nil {
		log.L().Error("fail to marshal config to toml", log.ShortError(err))
	}

	return b.String(), nil
}

// Parse parses flag definitions from the argument list.
func (c *SourceConfig) Parse(content string) error {
	// Parse first to get config file.
	metaData, err := toml.Decode(content, c)
	return c.check(&metaData, err)
}

// EncodeToml encodes config.
func (c *SourceConfig) EncodeToml() (string, error) {
	buf := new(bytes.Buffer)
	if err := toml.NewEncoder(buf).Encode(c); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (c *SourceConfig) String() string {
	cfg, err := json.Marshal(c)
	if err != nil {
		log.L().Error("fail to marshal config to json", log.ShortError(err))
	}
	return string(cfg)
}

func (c *SourceConfig) adjust() {
	c.From.Adjust()
	c.Checker.Adjust()
}

// Verify verifies the config
func (c *SourceConfig) Verify() error {
	if len(c.SourceID) == 0 {
		return terror.ErrWorkerNeedSourceID.Generate()
	}
	if len(c.SourceID) > MaxSourceIDLength {
		return terror.ErrWorkerTooLongSourceID.Generate(c.SourceID, MaxSourceIDLength)
	}

	var err error
	if c.EnableRelay {
		if len(c.RelayBinLogName) > 0 {
			if !binlog.VerifyFilename(c.RelayBinLogName) {
				return terror.ErrWorkerRelayBinlogName.Generate(c.RelayBinLogName)
			}
		}
		if len(c.RelayBinlogGTID) > 0 {
			_, err = gtid.ParserGTID(c.Flavor, c.RelayBinlogGTID)
			if err != nil {
				return terror.WithClass(terror.Annotatef(err, "relay-binlog-gtid %s", c.RelayBinlogGTID), terror.ClassDMWorker)
			}
		}
	}

	_, err = c.DecryptPassword()
	if err != nil {
		return err
	}

	return nil
}

// DecryptPassword returns a decrypted config replica in config
func (c *SourceConfig) DecryptPassword() (*SourceConfig, error) {
	clone := c.Clone()
	var (
		pswdFrom string
		err      error
	)
	if len(clone.From.Password) > 0 {
		pswdFrom, err = utils.Decrypt(clone.From.Password)
		if err != nil {
			return nil, terror.WithClass(err, terror.ClassDMWorker)
		}
	}
	clone.From.Password = pswdFrom
	return clone, nil
}

// GenerateDBConfig creates DBConfig for DB
func (c *SourceConfig) GenerateDBConfig() (*DBConfig, error) {
	// decrypt password
	clone, err := c.DecryptPassword()
	if err != nil {
		return nil, err
	}
	from := &clone.From
	from.RawDBCfg = DefaultRawDBConfig().SetReadTimeout(dbReadTimeout)
	return from, nil
}

// Adjust flavor and serverid of SourceConfig
func (c *SourceConfig) Adjust(db *sql.DB) (err error) {
	c.From.Adjust()
	c.Checker.Adjust()

	if c.Flavor == "" || c.ServerID == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), dbGetTimeout)
		defer cancel()

		err = c.AdjustFlavor(ctx, db)
		if err != nil {
			return err
		}

		err = c.AdjustServerID(ctx, db)
		if err != nil {
			return err
		}
	}

	return nil
}

// AdjustFlavor adjust Flavor from DB
func (c *SourceConfig) AdjustFlavor(ctx context.Context, db *sql.DB) (err error) {
	if c.Flavor != "" {
		switch c.Flavor {
		case mysql.MariaDBFlavor, mysql.MySQLFlavor:
			return nil
		default:
			return terror.ErrNotSupportedFlavor.Generate(c.Flavor)
		}
	}

	c.Flavor, err = utils.GetFlavor(ctx, db)
	if ctx.Err() != nil {
		err = terror.Annotatef(err, "time cost to get flavor info exceeds %s", dbGetTimeout)
	}
	return terror.WithScope(err, terror.ScopeUpstream)
}

// AdjustServerID adjust server id from DB
func (c *SourceConfig) AdjustServerID(ctx context.Context, db *sql.DB) error {
	if c.ServerID != 0 {
		return nil
	}

	serverIDs, err := getAllServerIDFunc(ctx, db)
	if ctx.Err() != nil {
		err = terror.Annotatef(err, "time cost to get server-id info exceeds %s", dbGetTimeout)
	}
	if err != nil {
		return terror.WithScope(err, terror.ScopeUpstream)
	}

	for i := 0; i < 5; i++ {
		randomValue := uint32(rand.Intn(100000))
		randomServerID := defaultBaseServerID + randomValue
		if _, ok := serverIDs[randomServerID]; ok {
			continue
		}

		c.ServerID = randomServerID
		return nil
	}

	return terror.ErrInvalidServerID.Generatef("can't find a random available server ID")
}

// LoadFromFile loads config from file.
func (c *SourceConfig) LoadFromFile(path string) error {
	metaData, err := toml.DecodeFile(path, c)
	return c.check(&metaData, err)
}

func (c *SourceConfig) check(metaData *toml.MetaData, err error) error {
	if err != nil {
		return terror.ErrWorkerDecodeConfigFromFile.Delegate(err)
	}
	undecoded := metaData.Undecoded()
	if len(undecoded) > 0 && err == nil {
		var undecodedItems []string
		for _, item := range undecoded {
			undecodedItems = append(undecodedItems, item.String())
		}
		return terror.ErrWorkerUndecodedItemFromFile.Generate(strings.Join(undecodedItems, ","))
	}
	c.adjust()
	return nil
}
