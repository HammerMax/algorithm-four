package raft

import "fmt"

func min(a, b uint64) uint64 {
	if a > b {
		return b
	}
	return a
}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func limitSize(ents []Entry, maxSize uint64) []Entry {
	if len(ents) == 0 {
		return ents
	}
	size := ents[0].Size()
	var limit int
	for limit = 1; limit < len(ents); limit++ {
		size += ents[limit].Size()
		if uint64(size) > maxSize {
			break
		}
	}
	return ents[:limit]
}

// PayloadSize is the size of the payload of this Entry. Notably, it does not
// depend on its Index or Term.
func PayloadSize(e Entry) int {
	return len(e.Data)
}

// voteResponseType maps vote and prevote message types to their corresponding responses.
func voteRespMsgType(msgt MessageType) MessageType {
	switch msgt {
	case MsgVote:
		return MsgVoteResp
	case MsgPreVote:
		return MsgPreVoteResp
	default:
		panic(fmt.Sprintf("not a vote message: %s", msgt))
	}
}