package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/andygrunwald/go-gerrit"
	"github.com/mattn/go-isatty"
	"golang.org/x/crypto/ssh/terminal"
)

const gerritInstance = "https://review.opendev.org"
const ciName = "Zuul"

var username = flag.String("u", "", "Gerrit username")

func getChangeID() (string, error) {
	if flag.NArg() != 1 {
		return "", fmt.Errorf("No change id specified")
	}

	changeIDStr := flag.Args()[0]

	changeID, err := strconv.Atoi(changeIDStr)
	if err != nil || changeID < 0 {
		return "", fmt.Errorf("Invalid change id %s", changeIDStr)
	}

	return changeIDStr, nil
}

func readPassword() (string, error) {
	fmt.Print("Gerrit password: ")
	defer fmt.Print("\n")

	if isatty.IsTerminal(os.Stdin.Fd()) {
		bytes, err := terminal.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return "", err
		}
		return string(bytes), nil
	}

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), err
}

func exitWithUsage(msg string) {
	fmt.Fprintf(os.Stderr, "%s\n", msg)
	fmt.Fprintf(os.Stderr, "Usage: gerrit-recheck -u <gerrit username> <gerrit change id>\n")
	flag.PrintDefaults()
	os.Exit(1)
}

func main() {
	flag.Parse()
	if *username == "" {
		exitWithUsage("Username not specified")
	}

	changeID, err := getChangeID()
	if err != nil {
		exitWithUsage(err.Error())
	}

	password, err := readPassword()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read password from stdin: %s", err.Error())
		os.Exit(1)
	}

	client, err := gerrit.NewClient(gerritInstance, nil)
	client.Authentication.SetBasicAuth(*username, password)

	change, _, err := client.Changes.GetChange(changeID, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get change details from gerrit: %s\n", err.Error())
		os.Exit(1)
	}
	log.Print(fmt.Sprintf("Rechecking change %s: %s", changeID, change.Subject))

	for {
		approved, err := doCheck(client, changeID)
		if err != nil {
			log.Print(fmt.Sprintf("ERROR: %s", err))
		}
		if approved {
			break
		}
		log.Print("Waiting for 30 minutes")
		time.Sleep(time.Minute * 30)
	}
}

func parseGerritDate(date string) (time.Time, error) {
	return time.Parse("2006-01-02 15:04:05.000000000", date)
}

func prettyDate(date time.Time) string {
	return date.Format("Mon 15:04:05")
}

func doCheck(client *gerrit.Client, changeID string) (bool, error) {
	change, _, err := client.Changes.GetChangeDetail(changeID, nil)
	if err != nil {
		return false, fmt.Errorf("Error fetching change details: %w", err)
	}
	log.Print("Fetched change details")

	verifications, ok := change.Labels["Verified"]
	if !ok {
		log.Print("No verification votes")
		return false, nil
	}

	var ciVote *gerrit.ApprovalInfo
	for _, approval := range verifications.All {
		if approval.Name == ciName {
			ciVote = &approval
			break
		}
	}
	if ciVote == nil {
		log.Print("CI has not voted")
		return false, nil
	}
	if ciVote.Value == 2 {
		log.Print("CI has approved")
		return true, nil
	}
	if ciVote.Value >= 0 {
		log.Print(fmt.Sprintf("CI voted %+d, waiting for approval", ciVote.Value))
		return false, nil
	}

	ciVoteDate, err := parseGerritDate(ciVote.Date)
	if err != nil {
		return false, fmt.Errorf("Error parsing CI vote date: %w", err)
	}

	log.Print(fmt.Sprintf("CI voted %+d at %s", ciVote.Value, prettyDate(ciVoteDate)))

	for _, msg := range change.Messages {
		if msg.Date.After(ciVoteDate) {
			for _, line := range strings.Split(msg.Message, "\n") {
				if strings.ToLower(strings.TrimSpace(line)) == "recheck" {
					log.Print(fmt.Sprintf("Rechecked by %s at %s", msg.Author.Name, prettyDate(msg.Date.Time)))
					return false, nil
				}
			}
		}
	}

	_, _, err = client.Changes.SetReview(changeID, "current", &gerrit.ReviewInput{Message: "recheck"})
	if err != nil {
		return false, fmt.Errorf("Adding review comment: %w", err)
	}
	log.Print("Added recheck")

	return false, nil
}
