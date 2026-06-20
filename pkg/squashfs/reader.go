package squashfs

import (
	"errors"
	"fmt"
	"io"

	"github.com/emmanuel-deloget/fsforge/pkg/compress"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
)

// Open reads an existing squashfs image into the agnostic tree, so squashfs can
// be a conversion source. Metadata (inode and directory tables) is decompressed
// into memory; file contents stay lazy, decompressed per block on demand.
func (e *Squashfs) Open(dev device.Device) (image.Image, error) {
	r, err := newSquashReader(dev)
	if err != nil {
		return nil, err
	}
	root, err := r.readInode(r.sb.rootInode, map[uint64]*image.Node{})
	if err != nil {
		return nil, err
	}
	return &sqImageRead{Mem: image.Adopt(e.deps, root)}, nil
}

// sqImageRead is an opened (read-only) squashfs image. Rebuild via convert to
// write changes; in-place re-finalize is not supported.
type sqImageRead struct{ *image.Mem }

func (sqImageRead) Finalize() error {
	return errors.New("squashfs: cannot re-finalize an opened image; rebuild via convert")
}

type fragEntry struct {
	start int64
	size  uint32
}

type squashReader struct {
	dev  device.Device
	sb   superblock
	comp compress.Compressor

	inodeData []byte
	inodeMap  map[uint32]int // compressed metablock start -> uncompressed offset
	dirData   []byte
	dirMap    map[uint32]int
	frags     []fragEntry
	ids       []uint32
}

func newSquashReader(dev device.Device) (*squashReader, error) {
	hdr := make([]byte, superblockSize)
	if _, err := dev.ReadAt(hdr, 0); err != nil && err != io.EOF {
		return nil, err
	}
	sb, err := parseSuperblock(hdr)
	if err != nil {
		return nil, err
	}
	if sb.compression != compress.GZIP {
		return nil, fmt.Errorf("squashfs: unsupported compressor id %d (only zlib/gzip)", sb.compression)
	}
	r := &squashReader{dev: dev, sb: sb, comp: compress.Zlib{}}

	var err2 error
	r.inodeData, r.inodeMap, err2 = r.readMetaTable(int64(sb.inodeTableStart), int64(sb.dirTableStart))
	if err2 != nil {
		return nil, err2
	}
	dirEnd := int64(sb.fragTableStart)
	if dirEnd < 0 || uint64(dirEnd) == noTable {
		dirEnd = int64(sb.idTableStart)
	}
	r.dirData, r.dirMap, err2 = r.readMetaTable(int64(sb.dirTableStart), dirEnd)
	if err2 != nil {
		return nil, err2
	}
	if err := r.readFragmentTable(); err != nil {
		return nil, err
	}
	if err := r.readIDTable(); err != nil {
		return nil, err
	}
	return r, nil
}

// readMetaTable decompresses the metadata blocks in [start,end) and returns the
// concatenated bytes plus a map from each block's compressed start (relative to
// start) to its offset in the decompressed output.
func (r *squashReader) readMetaTable(start, end int64) ([]byte, map[uint32]int, error) {
	var out []byte
	m := map[uint32]int{}
	pos := start
	for pos < end {
		rel := uint32(pos - start)
		m[rel] = len(out)
		block, next, err := r.readMetaBlock(pos)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, block...)
		pos = next
	}
	return out, m, nil
}

// readMetaBlock reads one metadata block at absolute offset off, returning its
// decompressed bytes and the offset of the next block.
func (r *squashReader) readMetaBlock(off int64) ([]byte, int64, error) {
	var h [2]byte
	if _, err := r.dev.ReadAt(h[:], off); err != nil {
		return nil, 0, err
	}
	hdr := le.Uint16(h[:])
	size := int(hdr &^ metaUncompressed)
	raw := make([]byte, size)
	if _, err := r.dev.ReadAt(raw, off+2); err != nil && err != io.EOF {
		return nil, 0, err
	}
	next := off + 2 + int64(size)
	if hdr&metaUncompressed != 0 {
		return raw, next, nil
	}
	out, err := r.comp.Decompress(nil, raw)
	return out, next, err
}

func (r *squashReader) readFragmentTable() error {
	if r.sb.fragments == 0 || uint64(r.sb.fragTableStart) == noTable {
		return nil
	}
	nBlocks := int((r.sb.fragments*16 + metaBlockSize - 1) / metaBlockSize)
	idx := make([]byte, nBlocks*8)
	if _, err := r.dev.ReadAt(idx, int64(r.sb.fragTableStart)); err != nil && err != io.EOF {
		return err
	}
	var data []byte
	for i := 0; i < nBlocks; i++ {
		block, _, err := r.readMetaBlock(int64(le.Uint64(idx[i*8:])))
		if err != nil {
			return err
		}
		data = append(data, block...)
	}
	for i := 0; i < int(r.sb.fragments); i++ {
		off := i * 16
		r.frags = append(r.frags, fragEntry{
			start: int64(le.Uint64(data[off:])),
			size:  le.Uint32(data[off+8:]),
		})
	}
	return nil
}

func (r *squashReader) readIDTable() error {
	if r.sb.noIDs == 0 {
		return nil
	}
	nBlocks := int((uint32(r.sb.noIDs)*4 + metaBlockSize - 1) / metaBlockSize)
	idx := make([]byte, nBlocks*8)
	if _, err := r.dev.ReadAt(idx, int64(r.sb.idTableStart)); err != nil && err != io.EOF {
		return err
	}
	var data []byte
	for i := 0; i < nBlocks; i++ {
		block, _, err := r.readMetaBlock(int64(le.Uint64(idx[i*8:])))
		if err != nil {
			return err
		}
		data = append(data, block...)
	}
	for i := 0; i < int(r.sb.noIDs); i++ {
		r.ids = append(r.ids, le.Uint32(data[i*4:]))
	}
	return nil
}

func (r *squashReader) idAt(i uint16) uint32 {
	if int(i) < len(r.ids) {
		return r.ids[i]
	}
	return 0
}

// inodeAt resolves a 48-bit inode reference to a byte offset in inodeData.
func (r *squashReader) inodeAt(ref uint64) (int, error) {
	block := uint32(ref >> 16)
	offset := int(ref & 0xFFFF)
	base, ok := r.inodeMap[block]
	if !ok {
		return 0, fmt.Errorf("squashfs: bad inode block ref %d", block)
	}
	return base + offset, nil
}
