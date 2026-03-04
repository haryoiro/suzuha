package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "suzuha-consolidator is deprecated: consolidation now runs inside suzuha-agent")
	os.Exit(1)
}
