package main

// Channel represents an IRC channel. It maps to Slack's groups and channels.
// Private messages are handled differently.
type Channel struct {
	Members []string
	Topic   string
	ID      string
	// Slack groups are different from channels. Here I try to uniform them for
	// IRC, but I still need to know which is which to use the right API calls.
	IsGroup bool
}

// MembersDiff compares the members of this channel with another members list
// and return a slice of members who joined and a slice of members who left.
func (c Channel) MembersDiff(otherMembers []string) ([]string, []string) {
	var membersMap = map[string]bool{}
	for _, m := range c.Members {
		membersMap[m] = true
	}
	var otherMembersMap = map[string]bool{}
	for _, m := range otherMembers {
		otherMembersMap[m] = true
	}

	added := make([]string, 0)
	for _, m := range otherMembers {
		if _, ok := membersMap[m]; !ok {
			added = append(added, m)
		}
	}

	removed := make([]string, 0)
	for _, m := range c.Members {
		if _, ok := otherMembersMap[m]; !ok {
			removed = append(removed, m)
		}
	}
	return added, removed
}
