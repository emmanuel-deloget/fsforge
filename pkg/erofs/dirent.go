package erofs

import "sort"

// dentry is one resolved directory entry: a name, the target inode's nid and
// the EROFS_FT_* type.
type dentry struct {
	name  string
	nid   uint64
	ftype uint8
}

// packDir serialises a directory's entries into EROFS directory blocks. Entries
// must be globally sorted by name (the kernel binary-searches both across
// blocks and within a block), so packDir sorts them and greedily fills each
// block: a block holds the erofs_dirent array first, then the names, then zero
// padding to the block boundary. Every block is padded to blockSize and the
// directory's i_size is the returned length (a whole number of blocks), which
// keeps the kernel's per-block maxsize == blockSize and lets a trailing NUL end
// the last name.
func packDir(entries []dentry) []byte {
	es := append([]dentry(nil), entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].name < es[j].name })

	var out []byte
	for i := 0; i < len(es); {
		// Greedily take as many entries as fit in one block.
		names := 0
		j := i
		for j < len(es) {
			n := j - i + 1
			if n*direntSize+names+len(es[j].name) > blockSize {
				break
			}
			names += len(es[j].name)
			j++
		}
		out = append(out, encodeDirBlock(es[i:j])...)
		i = j
	}
	return out
}

// encodeDirBlock lays out one block worth of entries, padded to blockSize.
func encodeDirBlock(es []dentry) []byte {
	block := make([]byte, blockSize)
	nameoff := len(es) * direntSize
	for k, e := range es {
		d := block[k*direntSize:]
		le.PutUint64(d[0:], e.nid)
		le.PutUint16(d[8:], uint16(nameoff))
		d[10] = e.ftype
		// d[11] reserved = 0
		copy(block[nameoff:], e.name)
		nameoff += len(e.name)
	}
	return block
}
