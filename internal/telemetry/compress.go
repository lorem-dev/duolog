package telemetry

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"
)

// faultHook lets tests stop compress after one of its steps: create, write,
// sync, close, rename or remove. compress then returns at once and leaves the
// files as a crash at that point would.
var faultHook func(step string) error

func fault(step string) error {
	if faultHook == nil {
		return nil
	}
	return faultHook(step)
}

// compress replaces the plain segment of a closed day with a gzip one. The
// caller holds s.mu and the partition lock. It writes day.ndjson.gz.tmp,
// syncs and closes it, renames it over day.ndjson.gz and removes the plain
// file. Until the rename the plain file is the segment: a step that fails
// returns at once and leaves it in place, with at most a .gz.tmp that the
// next prune removes. After the rename readers prefer the .gz, which is
// complete since it was synced first.
//
// A .gz already beside the plain file was left by a crash after its rename,
// or the day was written again after a clock was set back. When the .gz holds
// the plain file or a start of it, the plain file alone is the day; otherwise
// the .gz lines come first in the new copy.
func (s *Store) compress(partition, day string) error {
	today := s.o.Now().UTC().Format(time.DateOnly)
	if !partitionPattern.MatchString(partition) || !dayPattern.MatchString(day) || !validDay(day) || day >= today {
		return errRefused
	}
	plain := "v1/" + partition + "/" + day + ".ndjson"
	gz, tmp := plain+".gz", plain+".gz.tmp"
	read, kept, written, err := s.writeTemp(plain, gz, tmp)
	if err != nil {
		return err
	}
	if !kept {
		if err := retried(func() error { return s.root.Rename(tmp, gz) }); err != nil {
			return scrub(err)
		}
		if err := fault("rename"); err != nil {
			return err
		}
	}
	// The plain file goes only once the .gz in place is the copy of it, and
	// only if it is still the file that was read.
	at, err := s.root.Lstat(gz)
	if err != nil || !regular(at) || !os.SameFile(at, unwrap(written)) {
		return errUnsafe
	}
	at, err = s.root.Lstat(plain)
	if err != nil || !regular(at) || !os.SameFile(at, unwrap(read)) {
		return errUnsafe
	}
	if err := retried(func() error { return s.root.Remove(plain) }); err != nil {
		return scrub(err)
	}
	return fault("remove")
}

// writeTemp writes the gzip copy of plain to tmp, synced and closed, and
// returns the plain file it read and the file that is now the copy. When the
// .gz beside it already holds all of plain, kept is set, the copy is that .gz
// and nothing is written. Every handle is closed on return, so the files can
// be renamed and removed on Windows.
func (s *Store) writeTemp(plain, gz, tmp string) (read os.FileInfo, kept bool, written os.FileInfo, err error) {
	src, err := openReading(s.root, plain)
	if err != nil {
		return nil, false, nil, err
	}
	defer src.Close()
	if read, err = fileInfo(src); err != nil {
		return nil, false, nil, scrub(err)
	}
	var prior io.Reader // the .gz whose lines come first, if any
	old, err := openReading(s.root, gz)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, false, nil, err
	default:
		defer old.Close()
		info, err := fileInfo(old)
		if err != nil {
			return nil, false, nil, scrub(err)
		}
		prefix, whole, err := covers(old, src)
		if err != nil {
			// A .gz that does not decompress keeps both files as they are.
			return nil, false, nil, err
		}
		if whole {
			return read, true, info, nil
		}
		if !prefix {
			if _, err := old.Seek(0, io.SeekStart); err != nil {
				return nil, false, nil, scrub(err)
			}
			prior = old
		}
		if _, err := src.Seek(0, io.SeekStart); err != nil {
			return nil, false, nil, scrub(err)
		}
	}

	f, err := openRegular(s.root, tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return nil, false, nil, err
	}
	written, err = fileInfo(f)
	if err != nil {
		err = scrub(err)
	}
	if err == nil {
		err = fault("create")
	}
	if err == nil {
		err = writeGzip(f, prior, src)
	}
	if err == nil {
		err = fault("write")
	}
	if err == nil {
		err = scrub(f.Sync())
	}
	if err == nil {
		err = fault("sync")
	}
	if cerr := closeFile(f); err == nil {
		err = scrub(cerr)
	}
	if err == nil {
		err = fault("close")
	}
	return read, false, written, err
}

// maxRawCopy bounds what writeGzip reads out of an old .gz.
const maxRawCopy = 1024 * megabyte

// writeGzip writes the lines of the gzip stream old, when there is one, then
// plain, as one gzip stream to dst. A last line of old without its newline
// was cut by a crash; it is ended, so plain's first line stays whole.
func writeGzip(dst io.Writer, old, plain io.Reader) error {
	z := gzip.NewWriter(dst)
	if old != nil {
		r, err := gzip.NewReader(old)
		if err != nil {
			return err
		}
		end := &lastByte{w: z}
		// A day segment is at most a few megabytes raw; a .gz that expands
		// past maxRawCopy was not written by this store and is not copied.
		n, err := io.Copy(end, io.LimitReader(r, maxRawCopy+1))
		if err != nil {
			return err
		}
		if n > maxRawCopy {
			return errUnsafe
		}
		if end.n > 0 && end.last != '\n' {
			if _, err := z.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}
	if _, err := io.Copy(z, plain); err != nil {
		return err
	}
	return z.Close()
}

// lastByte passes writes on and keeps the last byte written.
type lastByte struct {
	w    io.Writer
	n    int64
	last byte
}

func (l *lastByte) Write(p []byte) (int, error) {
	n, err := l.w.Write(p)
	if n > 0 {
		l.n += int64(n)
		l.last = p[n-1]
	}
	return n, err
}

// covers decompresses gz against plain: prefix is true when gz holds the
// start of plain, whole when it holds all of it. A gz that fails to
// decompress or fails its checksum is an error.
func covers(gz, plain io.Reader) (prefix, whole bool, err error) {
	z, err := gzip.NewReader(gz)
	if err != nil {
		return false, false, err
	}
	defer z.Close()
	a, b := make([]byte, 32<<10), make([]byte, 32<<10)
	for {
		n, errA := io.ReadFull(z, a)
		if errA != nil && errA != io.EOF && errA != io.ErrUnexpectedEOF {
			return false, false, errA
		}
		m, errB := io.ReadFull(plain, b[:n])
		if errB != nil && errB != io.EOF && errB != io.ErrUnexpectedEOF {
			return false, false, errB
		}
		if m != n || !bytes.Equal(a[:n], b[:n]) {
			return false, false, nil
		}
		if errA != nil {
			// gz ended with a good checksum; plain may go on.
			more, err := plain.Read(b[:1])
			if err != nil && err != io.EOF {
				return false, false, err
			}
			return true, more == 0, nil
		}
	}
}
