package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Case interface {
	apply(key, val string)
}

type caseFunc func(key, val string)

func (f caseFunc) apply(key, val string) { f(key, val) }


func ConLoad(file string, cases ...Case) {
	f, err := os.Open(file)
	if err != nil {
		fmt.Printf("Unable to open config file %s\n", file)
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()

		if !strings.HasPrefix(line, "--") {
			continue
		}

		rest := strings.TrimSpace(strings.TrimPrefix(line, "--"))
		if rest == "" {
			continue
		}

		fields := strings.Fields(rest)
		if len(fields) < 2 {
			continue
		}

		key := fields[0]
		val := fields[1]

		for _, c := range cases {
			c.apply(key, val)
		}
	}

	// C macro ignores fgets errors; we’ll keep it similarly simple
}

func ConVal(key string, dest any, scanfFmt string) Case {
	return caseFunc(func(k, v string) {
		// C uses strcasecmp(_key, key)
		if !strings.EqualFold(k, key) {
			return
		}
		_, _ = fmt.Sscanf(v, scanfFmt, dest)
	})
}

func ConEnum[T any](key string, dest *T, matches ...EnumMatch[T]) Case {
	return caseFunc(func(k, v string) {
		if !strings.EqualFold(k, key) {
			return
		}
		var tmp T
		for _, m := range matches {
			if strings.EqualFold(v, m.name) {
				tmp = m.value
				*dest = tmp
				return
			}
		}
	})
}W

type EnumMatch[T any] struct {
	name  string
	value T
}

func ConMatch[T any](name string, value T) EnumMatch[T] {
	return EnumMatch[T]{name: name, value: value}
}
