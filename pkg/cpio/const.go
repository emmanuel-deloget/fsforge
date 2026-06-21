package cpio

// On-disk constants for the cpio "new ASCII" (newc) format — the format the
// Linux kernel consumes as an initramfs. See the kernel's init/initramfs.c and
// usr/gen_init_cpio.c. Every header field except the magic is an 8-digit,
// zero-padded, upper-case hexadecimal number.
const (
	magicNewc = "070701" // newc, no per-file checksum
	magicCRC  = "070702" // newc with checksum (read side accepts it)

	headerSize = 110 // 6-byte magic + 13 * 8-byte hex fields

	trailerName = "TRAILER!!!"

	// padTo is the trailing alignment GNU cpio applies to the whole archive so
	// concatenated archives stay block-aligned; the kernel tolerates any.
	padTo = 512
)

// Unix st_mode constants (cpio stores the full 32-bit st_mode).
const (
	sIFMT   = 0o170000
	sIFSOCK = 0o140000
	sIFLNK  = 0o120000
	sIFREG  = 0o100000
	sIFBLK  = 0o060000
	sIFDIR  = 0o040000
	sIFCHR  = 0o020000
	sIFIFO  = 0o010000

	sISUID = 0o4000
	sISGID = 0o2000
	sISVTX = 0o1000
)

// nAlign returns the on-disk size of the name region (name plus padding) for a
// name of namesize bytes, mirroring the kernel's N_ALIGN: the 110-byte header
// is congruent to 2 (mod 4), so the name region is rounded so that header plus
// name region is a multiple of 4 and the body starts aligned.
func nAlign(namesize int) int { return ((namesize + 1) &^ 3) + 2 }

// align4 rounds v up to the next multiple of four.
func align4(v int64) int64 { return (v + 3) &^ 3 }
