package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	fsforge "github.com/emmanuel-deloget/fsforge"
	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/partition"
	"github.com/emmanuel-deloget/fsforge/pkg/qcow2"
)

// disk builds a GPT or MBR disk with one or more partitions, each formatted
// with an engine and populated from a source directory.
func disk(args []string) error {
	fsSet := flag.NewFlagSet("disk", flag.ContinueOnError)
	output := fsSet.String("output", "", "output disk image file")
	sizeStr := fsSet.String("size", "", "total disk size, e.g. 512M, 2G")
	scheme := fsSet.String("scheme", "gpt", "partition scheme: gpt or mbr")
	reproducible := fsSet.Bool("reproducible", false, "deterministic output")
	var specs partFlags
	fsSet.Var(&specs, "part", "partition as <role>:<fstype>:<source>:<size>; repeatable. "+
		"role=esp|root|data, fstype=fat|ext2|ext4, size like 64M or 'rest'")
	if err := fsSet.Parse(args); err != nil {
		return err
	}
	if *output == "" || *sizeStr == "" || len(specs) == 0 {
		return fmt.Errorf("-output, -size and at least one -part are required")
	}

	total, err := fsforge.ParseSize(*sizeStr)
	if err != nil {
		return err
	}
	deps := fsforge.HostDeps()
	if *reproducible {
		deps = fsforge.ReproducibleDeps(fsforge.SourceDateEpoch())
	}

	f, err := os.Create(*output)
	if err != nil {
		return err
	}
	defer f.Close()

	// A .qcow2/.qcow output wraps the disk in a QCOW2 container; otherwise it is
	// a raw image. Either way the partition tables and engines see a plain
	// device of `total` bytes.
	var dev device.Device
	finalize := func() error { return nil }
	if fsforge.IsQcow2Path(*output) {
		qw, err := qcow2.NewWriter(f, total)
		if err != nil {
			return err
		}
		dev, finalize = qw, qw.Finalize
	} else {
		if err := f.Truncate(total); err != nil {
			return err
		}
		dev = device.NewFile(f, total)
	}

	parts, err := formatScheme(*scheme, dev, deps, specs)
	if err != nil {
		return err
	}

	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	for i, s := range specs {
		eng, err := fsforge.EngineFor(s.fstype, deps, 0)
		if err != nil {
			return err
		}
		img, err := eng.Format(parts[i].Section, image.Params{Label: s.role})
		if err != nil {
			return fmt.Errorf("format %s: %w", s.role, err)
		}
		closer, err := fsforge.PopulateFromDir(img.Root(), s.source)
		closers = append(closers, closer)
		if err != nil {
			return fmt.Errorf("populate %s: %w", s.role, err)
		}
		if err := img.Finalize(); err != nil {
			return fmt.Errorf("finalize %s: %w", s.role, err)
		}
		fmt.Printf("  partition %d (%s, %s): LBA %d-%d\n", i+1, s.role, s.fstype, parts[i].StartLBA, parts[i].EndLBA)
	}
	if err := finalize(); err != nil {
		return fmt.Errorf("finalize container: %w", err)
	}
	fmt.Printf("wrote %s disk %s\n", *scheme, *output)
	return nil
}

func formatScheme(scheme string, dev device.Device, deps image.Deps, specs partFlags) ([]partition.Partition, error) {
	switch scheme {
	case "gpt":
		pspecs := make([]partition.Spec, len(specs))
		for i, s := range specs {
			pspecs[i] = partition.Spec{Type: roleGUID(s.role), Name: s.role, Size: s.size}
		}
		return partition.FormatGPT(dev, deps, pspecs)
	case "mbr":
		mspecs := make([]partition.MBRSpec, len(specs))
		for i, s := range specs {
			mspecs[i] = partition.MBRSpec{Type: roleMBRType(s.role), Size: s.size, Bootable: s.role == "esp"}
		}
		return partition.FormatMBR(dev, mspecs)
	default:
		return nil, fmt.Errorf("unknown scheme %q (want gpt or mbr)", scheme)
	}
}

func roleGUID(role string) partition.Type {
	switch role {
	case "esp", "efi":
		return partition.TypeEFI
	case "root":
		return partition.TypeLinuxRoot
	default:
		return partition.TypeLinuxData
	}
}

func roleMBRType(role string) byte {
	switch role {
	case "esp", "efi":
		return partition.MBRTypeEFI
	default:
		return partition.MBRTypeLinux
	}
}

type partSpec struct {
	role, fstype, source string
	size                 int64
}

type partFlags []partSpec

func (p *partFlags) String() string { return "" }

func (p *partFlags) Set(v string) error {
	f := strings.SplitN(v, ":", 4)
	if len(f) != 4 {
		return fmt.Errorf("expected <role>:<fstype>:<source>:<size>, got %q", v)
	}
	var size int64
	if s := f[3]; s != "rest" && s != "0" && s != "" {
		n, err := fsforge.ParseSize(s)
		if err != nil {
			return err
		}
		size = n
	}
	*p = append(*p, partSpec{role: f[0], fstype: f[1], source: f[2], size: size})
	return nil
}
