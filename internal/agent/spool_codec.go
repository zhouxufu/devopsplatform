package agent

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func encodeQueueBatch(batch []PendingEvent) ([]byte, error) {
	b := make([]byte, 0, 4096)
	for _, p := range batch {
		for _, v := range []int64{p.Ev.Ts, p.Offset, p.Mtime} {
			b = binary.AppendVarint(b, v)
		}
		b = binary.AppendUvarint(b, p.Inode)
		if p.Reset {
			b = append(b, 1)
		} else {
			b = append(b, 0)
		}
		for _, v := range []string{p.Ev.ID, p.Ev.ApplicationID, p.Ev.Service, p.Ev.Environment, p.Ev.Host, p.Ev.Path, p.Ev.Level, p.Ev.TraceID, p.Ev.Message, p.Key, p.Head, p.Generation} {
			b = binary.AppendUvarint(b, uint64(len(v)))
			b = append(b, v...)
		}
		if len(b) > maxFrameBytes {
			return nil, fmt.Errorf("%w: queue batch exceeds %d encoded bytes", ErrSpoolFull, maxFrameBytes)
		}
	}
	return b, nil
}
func decodeQueueBatch(b []byte, count int, seq int64) ([]SpoolRecord, error) {
	result := make([]SpoolRecord, 0, count)
	for i := 0; i < count; i++ {
		start := len(b)
		var p PendingEvent
		for _, out := range []*int64{&p.Ev.Ts, &p.Offset, &p.Mtime} {
			v, n := binary.Varint(b)
			if n <= 0 {
				return nil, ErrSpoolCorrupt
			}
			*out = v
			b = b[n:]
		}
		inode, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, ErrSpoolCorrupt
		}
		p.Inode = inode
		b = b[n:]
		if len(b) == 0 || b[0] > 1 {
			return nil, ErrSpoolCorrupt
		}
		p.Reset = b[0] == 1
		b = b[1:]
		for _, out := range []*string{&p.Ev.ID, &p.Ev.ApplicationID, &p.Ev.Service, &p.Ev.Environment, &p.Ev.Host, &p.Ev.Path, &p.Ev.Level, &p.Ev.TraceID, &p.Ev.Message, &p.Key, &p.Head, &p.Generation} {
			size, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, ErrSpoolCorrupt
			}
			b = b[n:]
			if size > uint64(len(b)) {
				return nil, ErrSpoolCorrupt
			}
			*out = string(b[:int(size)])
			b = b[int(size):]
		}
		result = append(result, SpoolRecord{Seq: seq + int64(i), Bytes: int64(start - len(b) + 128), Pending: p})
	}
	if len(b) != 0 {
		return nil, ErrSpoolCorrupt
	}
	return result, nil
}

type queueJSON struct {
	Data   json.RawMessage `json:"data"`
	SHA256 string          `json:"sha256"`
}

func writeQueueJSON(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	raw, err := json.Marshal(queueJSON{b, hex.EncodeToString(sum[:])})
	if err != nil {
		return err
	}
	return writeQueueFile(path, raw)
}
func readQueueJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > 16<<20 {
		return ErrSpoolCorrupt
	}
	b := make([]byte, st.Size())
	if _, err = f.ReadAt(b, 0); err != nil {
		return err
	}
	var envelope queueJSON
	if err = json.Unmarshal(b, &envelope); err != nil {
		return err
	}
	sum := sha256.Sum256(envelope.Data)
	if envelope.SHA256 != hex.EncodeToString(sum[:]) {
		return errors.New("queue metadata checksum mismatch")
	}
	return json.Unmarshal(envelope.Data, value)
}
func writeQueueFile(path string, b []byte) error {
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		return err
	}
	return syncQueueDir(filepath.Dir(path))
}
func syncQueueDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
