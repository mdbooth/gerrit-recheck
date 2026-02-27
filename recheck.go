package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/andygrunwald/go-gerrit"
	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

const gerritInstance = "https://review.opendev.org"
const ciName = "Zuul"

var username = flag.String("u", "", "Gerrit username")
var dryRun = flag.Bool("dry-run", false, "Don't post recheck messages")

func getChangeNumbers() ([]string, error) {
	changeNumbers := []string{}

	if flag.NArg() < 1 {
		return changeNumbers, fmt.Errorf("no change number specified")
	}

	for i := 0; i < flag.NArg(); i++ {
		changeNumberStr := flag.Args()[i]

		changeNumber, err := strconv.Atoi(changeNumberStr)
		if err != nil || changeNumber < 0 {
			return changeNumbers, fmt.Errorf("invalid change number %s", changeNumberStr)
		}
		changeNumbers = append(changeNumbers, changeNumberStr)
	}
	return changeNumbers, nil
}

func readPassword() (string, error) {
	fmt.Print("Gerrit password: ")
	defer fmt.Print("\n")

	if isatty.IsTerminal(os.Stdin.Fd()) {
		bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
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
	fmt.Fprintf(os.Stderr, "Usage: gerrit-recheck -u <USERNAME> [--dry-run] <CHANGE_ID> [<CHANGE_ID>...]\n")
	flag.PrintDefaults()
	os.Exit(1)
}

func main() {
	flag.Parse()
	if *username == "" {
		exitWithUsage("Username not specified")
	}

	changeNumbers, err := getChangeNumbers()
	if err != nil {
		exitWithUsage(err.Error())
	}

	password, err := readPassword()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read password from stdin: %s", err.Error())
		os.Exit(1)
	}

	client, err := gerrit.NewClient(context.TODO(), gerritInstance, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Gerrit client: %s\n", err.Error())
		os.Exit(1)
	}

	client.Authentication.SetBasicAuth(*username, password)

	// mapping of change ID to merge status
	changeMergeStatus := make(map[string]bool)

	for _, changeNumber := range changeNumbers {
		change, _, err := client.Changes.GetChange(context.TODO(), changeNumber, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get change details from Gerrit: %s\n", err.Error())
			os.Exit(1)
		}

		changeMergeStatus[change.ChangeID] = false

		// GetRelatedChanges expects a string revision number despite it being an integer
		relatedChanges, _, err := client.Changes.GetRelatedChanges(context.TODO(), changeNumber, strconv.Itoa(change.CurrentRevisionNumber))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get change details from Gerrit: %s\n", err.Error())
			os.Exit(1)
		}

		foundChange := false
		for _, relatedChange := range relatedChanges.Changes {
			if !foundChange {
				// we want to skip dependent patches as we only want dependencies
				if relatedChange.ChangeID == change.ChangeID {
					foundChange = true
				}
				continue
			}
			changeMergeStatus[relatedChange.ChangeID] = false
		}
	}

	for {
		remainingChanges := false

		for changeID, changeMerged := range changeMergeStatus {
			if changeMerged {
				continue
			}

			approved, err := doCheck(client, changeID, *dryRun)
			if err != nil {
				log.Printf("ERROR: %s", err)
			}

			if approved {
				changeMergeStatus[changeID] = true
			} else {
				remainingChanges = true
			}
			fmt.Println()
		}

		if !remainingChanges {
			log.Println("All changes have merged. Exiting...")
			break
		}

		if *dryRun {
			log.Println("Running in dry-run mode. Exiting...")
			break
		}

		log.Println("Waiting for 30 minutes")
		time.Sleep(time.Minute * 30)
	}
}

func parseGerritDate(date string) (time.Time, error) {
	return time.Parse("2006-01-02 15:04:05.000000000", date)
}

func prettyDate(date time.Time) string {
	return date.Format("Mon 15:04:05")
}

func doCheck(client *gerrit.Client, changeNumber string, dryRun bool) (bool, error) {
	change, _, err := client.Changes.GetChangeDetail(context.TODO(), changeNumber, nil)
	if err != nil {
		return false, fmt.Errorf("error fetching change details: %w", err)
	}
	log.Printf("Fetched change %s details: %s: %s", changeNumber, change.ChangeID, change.Subject)

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
		log.Printf("CI voted %+d, waiting for approval", ciVote.Value)
		return false, nil
	}

	ciVoteDate, err := parseGerritDate(ciVote.Date)
	if err != nil {
		return false, fmt.Errorf("error parsing CI vote date: %w", err)
	}

	var recheckAuthor string
	var recheckDate time.Time
	for _, msg := range change.Messages {
		if msg.Date.After(ciVoteDate) {
			if msg.Tag == "" {
				for line := range strings.SplitSeq(msg.Message, "\n") {
					if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "recheck") {
						recheckDate = msg.Date.Time
						recheckAuthor = msg.Author.Name
					}
				}
			} else if msg.Date.After(recheckDate) && strings.HasPrefix(msg.Tag, "autogenerated:zuul:") {
				// Gerrit will not update the date on Zuul's vote
				// if Zuul votes -1 again after a recheck, so we
				// need to explicitly look for "Build failed"
				// messages.
				for line := range strings.SplitSeq(msg.Message, "\n") {
					if strings.HasPrefix(line, "Build failed") {
						ciVoteDate = msg.Date.Time
						recheckDate = time.Time{}
					}
				}
			}
		}
	}

	log.Printf("CI voted %+d at %s", ciVote.Value, prettyDate(ciVoteDate))

	if recheckDate.After(ciVoteDate) {
		log.Printf("Skipping comment: Rechecked by %s at %s", recheckAuthor, prettyDate(recheckDate))
		return false, nil
	}

	if !dryRun {
		_, _, err = client.Changes.SetReview(context.TODO(), changeNumber, "current", &gerrit.ReviewInput{Message: "recheck"})
		if err != nil {
			return false, fmt.Errorf("error adding review comment: %w", err)
		}
		log.Print("Added recheck")
	} else {
		log.Print("Skipping comment: Dry run mode enabled")
	}

	return false, nil
}
