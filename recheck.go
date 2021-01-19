package main

import (
	"fmt"
	"github.com/andygrunwald/go-gerrit"
	"github.com/jdxcode/netrc"
	"log"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const gerritInstance = "https://review.opendev.org"
const ciName = "Zuul"

func getChangeID() (string, error) {
	const usage = "Usage: gerrit-recheck <changeid>"

	if len(os.Args) != 2 {
		return "", fmt.Errorf(usage)
	}

	changeIDStr := os.Args[1]

	changeID, err := strconv.Atoi(changeIDStr)
	if err != nil || changeID < 0 {
		return "", fmt.Errorf("Invalid change id %s\n%s", changeIDStr, usage)
	}

	return changeIDStr, nil
}

func main() {
	changeID, err := getChangeID()
	if err != nil {
		panic(err)
	}

	usr, err := user.Current()
	n, err := netrc.Parse(filepath.Join(usr.HomeDir, ".netrc"))
	if err != nil {
		panic(fmt.Errorf("Parsing .netrc: %w", err))
	}

	url, err := url.Parse(gerritInstance)
	if err != nil {
		panic(err)
	}

	machine := n.Machine(url.Host)
	if machine == nil {
		panic(fmt.Errorf("%s not found in .netrc", url.Host))
	}
	username := machine.Get("login")
	password := machine.Get("password")

	client, err := gerrit.NewClient(gerritInstance, nil)
	client.Authentication.SetBasicAuth(username, password)

	for {
		approved, err := doCheck(client, changeID)
		if err != nil {
			log.Print(fmt.Sprintf("ERROR: %s", err))
			continue
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
		log.Print("No verfication votes")
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
	if ciVote.Value == 1 {
		log.Print("CI voted +1, waiting for approval")
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
