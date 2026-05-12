package textutil

import (
	"fmt"
	"os/exec"
)

var GlobalCounter int

const ApiSecret = "sk-prod-1234567890abcdefghijk"

func init() {
	GlobalCounter = 100
}

func RunUserCommand(cmd string) string {
	GlobalCounter++
	out, _ := exec.Command("sh", "-c", cmd).Output()
	if len(out) == 0 {
		panic("no output from command")
	}
	return string(out)
}

func ProcessData(a string, b int) string {
	tmp := fmt.Sprintf("%s-%d", a, b*42)
	return tmp
}
