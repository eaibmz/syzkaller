// Copyright 2016 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/google/syzkaller/pkg/osutil"
	. "github.com/google/syzkaller/sys/sysparser"
)

var (
	flagLinux    = flag.String("linux", "", "path to linux kernel source checkout")
	flagLinuxBld = flag.String("linuxbld", "", "path to linux kernel build directory")
	flagArch     = flag.String("arch", "", "arch to generate")
	flagV        = flag.Int("v", 0, "verbosity")
)

type Arch struct {
	CARCH            []string
	KernelHeaderArch string
	KernelInclude    string
	CFlags           []string
}

var archs = map[string]*Arch{
	"amd64":   {[]string{"__x86_64__"}, "x86", "asm/unistd.h", []string{"-m64"}},
	"arm64":   {[]string{"__aarch64__"}, "arm64", "asm/unistd.h", []string{}},
	"ppc64le": {[]string{"__ppc64__", "__PPC64__", "__powerpc64__"}, "powerpc", "asm/unistd.h", []string{"-D__powerpc64__"}},
}

func main() {
	flag.Parse()
	if *flagLinux == "" {
		failf("provide path to linux kernel checkout via -linux flag (or make extract LINUX= flag)")
	}
	if *flagLinuxBld == "" {
		logf(1, "No kernel build directory provided, assuming in-place build")
		*flagLinuxBld = *flagLinux
	}
	if *flagArch == "" {
		failf("-arch flag is required")
	}
	if archs[*flagArch] == nil {
		failf("unknown arch %v", *flagArch)
	}
	if len(flag.Args()) != 1 {
		failf("usage: syz-extract -linux=/linux/checkout -arch=arch sys/input_file.txt")
	}

	inname := flag.Args()[0]
	outname := strings.TrimSuffix(inname, ".txt") + "_" + *flagArch + ".const"

	inf, err := os.Open(inname)
	if err != nil {
		failf("failed to open input file: %v", err)
	}
	defer inf.Close()

	desc := Parse(inf)
	consts := compileConsts(archs[*flagArch], desc)

	out := new(bytes.Buffer)
	generateConsts(*flagArch, consts, out)
	if err := osutil.WriteFile(outname, out.Bytes()); err != nil {
		failf("failed to write output file: %v", err)
	}
}

func generateConsts(arch string, consts map[string]uint64, out io.Writer) {
	var nv []NameValue
	for k, v := range consts {
		nv = append(nv, NameValue{k, v})
	}
	sort.Sort(NameValueArray(nv))

	fmt.Fprintf(out, "# AUTOGENERATED FILE\n")
	for _, x := range nv {
		fmt.Fprintf(out, "%v = %v\n", x.name, x.val)
	}
}

func compileConsts(arch *Arch, desc *Description) map[string]uint64 {
	vals := make(map[string]bool)
	for _, fvals := range desc.Flags {
		for _, v := range fvals {
			vals[v] = true
		}
	}
	for v := range desc.Defines {
		vals[v] = true
	}
	for _, sc := range desc.Syscalls {
		if strings.HasPrefix(sc.CallName, "syz_") {
			continue
		}
		name := "__NR_" + sc.CallName
		vals[name] = true
	}
	for _, res := range desc.Resources {
		for _, v := range res.Values {
			vals[v] = true
		}
	}

	valArr := make([]string, 0, len(vals))
	for v := range vals {
		if !isIdentifier(v) {
			continue
		}
		valArr = append(valArr, v)
	}
	if len(valArr) == 0 {
		return nil
	}

	consts, err := fetchValues(arch.KernelHeaderArch, valArr, append(desc.Includes, arch.KernelInclude), desc.Incdirs, desc.Defines, arch.CFlags)
	if err != nil {
		failf("%v", err)
	}
	return consts
}

func isIdentifier(s string) bool {
	for i, c := range s {
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || i > 0 && (c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

type NameValue struct {
	name string
	val  uint64
}

type NameValueArray []NameValue

func (a NameValueArray) Len() int           { return len(a) }
func (a NameValueArray) Less(i, j int) bool { return a[i].name < a[j].name }
func (a NameValueArray) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

func failf(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, msg+"\n", args...)
	os.Exit(1)
}

func logf(v int, msg string, args ...interface{}) {
	if *flagV >= v {
		fmt.Fprintf(os.Stderr, msg+"\n", args...)
	}
}
