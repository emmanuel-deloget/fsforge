package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/emmanuel-deloget/fsforge/pkg/device"
	"github.com/emmanuel-deloget/fsforge/pkg/image"
	"github.com/emmanuel-deloget/fsforge/pkg/partition"
)

// disk builds a GPT disk with one or more partitions, each formatted with an
// engine and populated from a source directory.
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

	total, err := parseSize(*sizeStr)
	if err != nil {
		return err
	}
	deps := buildDeps(*reproducible)

	f, err := os.Create(*output)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(total); err != nil {
		return err
	}
	dev := device.NewFile(f, total)

	var parts []partition.Partition
	switch *scheme {
	case "gpt":
		pspecs := make([]partition.Spec, len(specs))
		for i, s := range specs {
			pspecs[i] = partition.Spec{Type: roleGUID(s.role), Name: s.role, Size: s.size}
		}
		parts, err = partition.FormatGPT(dev, deps, pspecs)
	case "mbr":
		mspecs := make([]partition.MBRSpec, len(specs))
		for i, s := range specs {
			mspecs[i] = partition.MBRSpec{Type: roleMBRType(s.role), Size: s.size, Bootable: s.role == "esp"}
		}
		parts, err = partition.FormatMBR(dev, mspecs)
	default:
		return fmt.Errorf("unknown scheme %q (want gpt or mbr)", *scheme)
	}
	if err != nil {
		return err
	}

	var closers []*os.File
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	for i, s := range specs {
		eng, err := engineFor(s.fstype, deps, 0)
		if err != nil {
			return err
		}
		img, err := eng.Format(parts[i].Section, image.Params{Label: s.role})
		if err != nil {
			return fmt.Errorf("format %s: %w", s.role, err)
		}
		cs, err := populate(img.Root(), s.source)
		closers = append(closers, cs...)
		if err != nil {
			return fmt.Errorf("populate %s: %w", s.role, err)
		}
		if err := img.Finalize(); err != nil {
			return fmt.Errorf("finalize %s: %w", s.role, err)
		}
		fmt.Printf("  partition %d (%s, %s): LBA %d-%d\n", i+1, s.role, s.fstype, parts[i].StartLBA, parts[i].EndLBA)
	}
	fmt.Printf("wrote GPT disk %s\n", *output)
	return nil
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
		n, err := parseSize(s)
		if err != nil {
			return err
		}
		size = n
	}
	*p = append(*p, partSpec{role: f[0], fstype: f[1], source: f[2], size: size})
	return nil
}
