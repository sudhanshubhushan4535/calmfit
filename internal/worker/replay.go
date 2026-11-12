package worker

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// ReplayGroup is the consumer group of the DLQ replay command. Its committed offsets mark how
// far the DLQ has been replayed.
const ReplayGroup = "calmfit-dlq-replay"

// HeaderReplayedFrom is added to a replayed record: "<dlq topic>/<partition>@<offset>".
const HeaderReplayedFrom = "replayed-from-dlq"

// Replayer moves DLQ records back to the main topic after the bug that sent them there is fixed.
// It reads only up to the DLQ's end offsets taken at start, so a record that fails again and
// lands back in the DLQ during the run is not replayed in a loop.
type Replayer struct {
	Client *kgo.Client // consumer of the DLQ in Group (see NewConsumer)
	Admin  *kadm.Client
	Group  string // must match Client's group; "" means ReplayGroup
	DLQ    string
	Topic  string    // where records are re-published
	DryRun bool      // print only: nothing produced or committed
	Max    int       // 0 means no limit
	Out    io.Writer // one line per record
}

// Run replays (or, with DryRun, lists) pending DLQ records and returns how many it handled.
// Each record is re-produced with its original key and value; its DLQ offset is committed only
// after the produce is acknowledged, so a crash can replay a record twice but never lose one.
// Replaying twice is harmless: the worker dedupes on event_id.
func (rp *Replayer) Run(ctx context.Context) (int, error) {
	ends, err := rp.Admin.ListEndOffsets(ctx, rp.DLQ)
	if err != nil {
		return 0, fmt.Errorf("dlq end offsets: %w", err)
	}
	if err := ends.Error(); err != nil {
		return 0, fmt.Errorf("dlq end offsets: %w", err)
	}
	group := rp.Group
	if group == "" {
		group = ReplayGroup
	}
	committed, err := rp.Admin.FetchOffsets(ctx, group)
	if err != nil {
		return 0, fmt.Errorf("replay group offsets: %w", err)
	}
	pending := map[int32]int64{} // partition → end offset, for partitions with unreplayed records
	ends.Each(func(o kadm.ListedOffset) {
		at := int64(0)
		if c, ok := committed.Lookup(o.Topic, o.Partition); ok && c.At >= 0 {
			at = c.At
		}
		if at < o.Offset {
			pending[o.Partition] = o.Offset
		}
	})

	n := 0
	for len(pending) > 0 && (rp.Max == 0 || n < rp.Max) {
		fs := rp.Client.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if err := fs.Err0(); err != nil {
			return n, fmt.Errorf("poll dlq: %w", err)
		}
		var batch []*kgo.Record
		for _, r := range fs.Records() {
			end, ok := pending[r.Partition]
			if !ok || r.Offset >= end || (rp.Max > 0 && n+len(batch) >= rp.Max) {
				continue // arrived after we started, or over the limit: left for the next run
			}
			batch = append(batch, r)
			if r.Offset+1 >= end {
				delete(pending, r.Partition)
			}
		}
		for _, r := range batch {
			fmt.Fprintln(rp.Out, describe(r))
		}
		if !rp.DryRun && len(batch) > 0 {
			out := make([]*kgo.Record, len(batch))
			for i, r := range batch {
				out[i] = &kgo.Record{Topic: rp.Topic, Key: r.Key, Value: r.Value, Headers: []kgo.RecordHeader{{
					Key: HeaderReplayedFrom, Value: []byte(r.Topic + "/" + strconv.Itoa(int(r.Partition)) + "@" + strconv.FormatInt(r.Offset, 10)),
				}}}
			}
			if err := rp.Client.ProduceSync(ctx, out...).FirstErr(); err != nil {
				return n, fmt.Errorf("re-produce: %w", err)
			}
			if err := rp.Client.CommitRecords(ctx, batch...); err != nil {
				return n, fmt.Errorf("commit dlq offsets: %w", err)
			}
		}
		rp.Client.AllowRebalance()
		n += len(batch)
	}
	return n, nil
}

// describe prints a record's DLQ headers and a prefix of its value.
func describe(r *kgo.Record) string {
	h := map[string]string{}
	for _, x := range r.Headers {
		h[x.Key] = string(x.Value)
	}
	v := string(r.Value)
	if len(v) > 200 {
		v = v[:200] + "…"
	}
	return fmt.Sprintf("%s/%d@%d key=%s attempts=%s from=%s/%s@%s failed_at=%s\n  error: %s\n  value: %s",
		r.Topic, r.Partition, r.Offset, r.Key, h[HeaderAttempts], h[HeaderSrcTopic], h[HeaderSrcPartition],
		h[HeaderSrcOffset], h[HeaderFailedAt], h[HeaderError], v)
}
