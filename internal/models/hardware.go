package models

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

const (
	mib = 1024 * 1024
	gib = 1024 * mib
)

type Hardware struct {
	Chip      string `json:"chip"`
	MemBytes  uint64 `json:"mem_bytes"`
	MemGiB    uint64 `json:"mem_gib"`
	CPUCount  int    `json:"cpu_count"`
}

type Defaults struct {
	ASREngine    string
	TranslatorID string
}

// DetectHardware reads Apple Silicon hardware specs via sysctl without cgo.
func DetectHardware() Hardware {
	hw := Hardware{
		Chip:     sysctlString("machdep.cpu.brand_string"),
		CPUCount: runtime.NumCPU(),
	}
	if memStr := sysctlString("hw.memsize"); memStr != "" {
		if v, err := strconv.ParseUint(memStr, 10, 64); err == nil {
			hw.MemBytes = v
			hw.MemGiB = (v + gib/2) / gib
		}
	}
	if ncpuStr := sysctlString("hw.ncpu"); ncpuStr != "" {
		if n, err := strconv.Atoi(ncpuStr); err == nil && n > 0 {
			hw.CPUCount = n
		}
	}
	return hw
}

// SelectDefaults picks default ASR and translator models for Apple Silicon memory size.
// - 8 GB Macs (< 16 GiB - 512 MiB): PLaMo 3 Translate 2B Q4_K_M + Parakeet
// - 16 GB+ Macs (>= 16 GiB - 512 MiB): PLaMo 3 Translate 8B Q4_K_M + Whisper (provisional until benchmark decides)
func SelectDefaults(memBytes uint64) Defaults {
	const threshold16GiB = 16*gib - 512*mib
	if memBytes >= threshold16GiB {
		return Defaults{
			ASREngine:    ASRWhisper,
			TranslatorID: ModelPlamo8BQ4KM,
		}
	}
	return Defaults{
		ASREngine:    ASRParakeet,
		TranslatorID: ModelPlamo2BQ4KM,
	}
}

func sysctlString(key string) string {
	out, err := exec.Command("sysctl", "-n", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
