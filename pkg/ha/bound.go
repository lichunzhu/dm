// Copyright 2020 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package ha

import (
	"context"
	"encoding/json"
	"fmt"

	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/mvcc/mvccpb"
	"go.uber.org/zap"

	"github.com/pingcap/dm/dm/common"
	"github.com/pingcap/dm/pkg/etcdutil"
	"github.com/pingcap/dm/pkg/log"
)

// SourceBound represents the bound relationship between the DM-worker instance and the upstream MySQL source.
type SourceBound struct {
	Source string `json:"source"` // the source ID of the upstream.
	Worker string `json:"worker"` // the name of the bounded DM-worker for the source.
}

// NewSourceBound creates a new SourceBound instance.
func NewSourceBound(source, worker string) SourceBound {
	return SourceBound{
		Source: source,
		Worker: worker,
	}
}

// NotBound returns whether the relationship has not bound.
// An empty bound means the relationship has not bound.
func (b SourceBound) NotBound() bool {
	return b.Source == "" && b.Worker == ""
}

// String implements Stringer interface.
func (b SourceBound) String() string {
	s, _ := b.toJSON()
	return s
}

// toJSON returns the string of JSON represent.
func (b SourceBound) toJSON() (string, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// sourceBoundFromJSON constructs SourceBound from its JSON represent.
func sourceBoundFromJSON(s string) (b SourceBound, err error) {
	err = json.Unmarshal([]byte(s), &b)
	return
}

// PutSourceBound puts the bound relationship into etcd.
// k/v: worker-name -> bound relationship.
func PutSourceBound(cli *clientv3.Client, bound SourceBound) (int64, error) {
	op, err := putSourceBoundOp(bound)
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(cli.Ctx(), etcdutil.DefaultRequestTimeout)
	defer cancel()

	resp, err := cli.Txn(ctx).Then(op).Commit()
	if err != nil {
		return 0, err
	}
	return resp.Header.Revision, nil
}

// GetSourceBound gets the source bound relationship for the specified DM-worker.
func GetSourceBound(cli *clientv3.Client, worker string) (SourceBound, int64, error) {
	ctx, cancel := context.WithTimeout(cli.Ctx(), etcdutil.DefaultRequestTimeout)
	defer cancel()

	bound := SourceBound{}
	resp, err := cli.Get(ctx, common.UpstreamBoundWorkerKeyAdapter.Encode(worker))
	if err != nil {
		return bound, 0, err
	}

	if resp.Count == 0 {
		return bound, 0, nil
	} else if resp.Count > 1 {
		// TODO(csuzhangxc): add terror.
		// this should not happen.
		return bound, 0, fmt.Errorf("too many bound relationship (%d) exist for the DM-worker %s", resp.Count, worker)
	}

	bound, err = sourceBoundFromJSON(string(resp.Kvs[0].Value))
	if err != nil {
		return bound, 0, err
	}

	return bound, resp.Header.Revision, nil
}

// WatchSourceBound watches PUT & DELETE operations for the bound relationship of the specified DM-worker.
// For the DELETE operations, it returns an empty bound relationship.
func WatchSourceBound(ctx context.Context, cli *clientv3.Client,
	worker string, revision int64, outCh chan<- SourceBound) {
	ch := cli.Watch(ctx, common.UpstreamBoundWorkerKeyAdapter.Encode(worker), clientv3.WithRev(revision))

	for {
		select {
		case <-ctx.Done():
			return
		case resp := <-ch:
			if resp.Canceled {
				return
			}

			for _, ev := range resp.Events {
				var (
					bound SourceBound
					err   error
				)
				switch ev.Type {
				case mvccpb.PUT:
					bound, err = sourceBoundFromJSON(string(ev.Kv.Value))
					if err != nil {
						// this should not happen.
						log.L().Error("fail to construct source bound relationship", zap.ByteString("json", ev.Kv.Value))
						continue
					}
				case mvccpb.DELETE:
				default:
					// this should not happen.
					log.L().Error("unsupported etcd event type", zap.Reflect("kv", ev.Kv), zap.Reflect("type", ev.Type))
					continue
				}

				select {
				case outCh <- bound:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// deleteSourceBoundOp returns a DELETE ectd operation for the bound relationship of the specified DM-worker.
func deleteSourceBoundOp(worker string) clientv3.Op {
	return clientv3.OpDelete(common.UpstreamBoundWorkerKeyAdapter.Encode(worker))
}

// putSourceBoundOp returns a PUT etcd operation for the bound relationship.
// k/v: worker-name -> bound relationship.
func putSourceBoundOp(bound SourceBound) (clientv3.Op, error) {
	value, err := bound.toJSON()
	if err != nil {
		return clientv3.Op{}, err
	}
	key := common.UpstreamBoundWorkerKeyAdapter.Encode(bound.Worker)

	return clientv3.OpPut(key, value), nil
}