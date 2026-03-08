package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: ctl <command>")
		fmt.Println()
		fmt.Println("commands:")
		fmt.Println("  status        Show system status (desks, positions, P&L)")
		fmt.Println("  desks         List all desks with performance summary")
		fmt.Println("  pnl           Show portfolio P&L breakdown")
		fmt.Println("  positions     List all open positions")
		fmt.Println("  beliefs       Show belief graph state")
		fmt.Println("  engrams       List active engrams")
		fmt.Println("  anti          Show anti-portfolio analysis")
		fmt.Println("  regime        Show current market regime")
		fmt.Println("  ab            Show A/B test comparison")
		fmt.Println("  kill-switch   Trigger emergency halt")
		fmt.Println("  resume        Resume trading after kill switch")
		os.Exit(1)
	}

	// TODO: Connect to running floor instance and execute commands
	fmt.Printf("ctl: command '%s' not yet implemented\n", os.Args[1])
}
