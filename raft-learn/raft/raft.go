package raft

import (
	"bytes"
	"errors"
	"etcd/raft/quorum"
	"etcd/raft/tracker"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"
)

const None uint64 = 0
const noLimit = math.MaxUint64

// ErrProposalDropped is returned when the proposal is ignored by some cases,
// so that the proposer can be notified and fail fast.
var ErrProposalDropped = errors.New("raft proposal dropped")

// Possible values for StateType.
const (
	StateFollower StateType = iota
	StateCandidate
	StateLeader
	StatePreCandidate
)

type ReadOnlyOption int

const (
	ReadOnlySafe ReadOnlyOption = iota
	ReadOnlyLeaseBased
)

// StateType 表明该节点的身份
type StateType uint64

type raft struct {
	// 当前节点在集群中的ID
	id uint64

	// 任期
	Term uint64

	// 当前任期中，选票投给的节点
	Vote uint64

	raftLog *raftLog

	state StateType

	// 每条消息最大字节数
	maxMsgSize uint64
	maxUncommittedSize uint64

	// 带发送的消息切片
	msgs []Message

	// Leader节点会记录集群中其他节点的日志复制情况（NextIndex和MatchIndex）

	// leaderID
	lead uint64

	// leadTransferee是即将要成为的LeaderID
	leadTransferee uint64

	// TODO 什么意思
	pendingConfIndex uint64

	uncommittedSize uint64

	isLearner bool

	prs tracker.ProgressTracker

	heartbeatTimeout int
	electionTimeout int

	// 是否开启checkQuorum模式。
	// 当发生网络分区现象时，同一时刻会出现多个Leader。旧的leader会根据心跳的ack检查
	// follower是否达到半数。如果没有达到则becomeFollower。
	// checkQuorum 为 true，就是开启定时检测是否与集群中多数节点相连
	checkQuorum bool
	preVote bool

	readOnly *readOnly

	// 流逝的时间。raft自己维护的计时器
	electionElapsed int
	heartbeatElapsed int

	// randomizedElectionTimeout between [electiontimeout, 2 * electiontimeout -1]
	// 真正的选举超时时间
	randomizedElectionTimeout int
	disableProposalForwarding bool

	// 因为electionTick 与 heartbeatTick 同一时间只会存在一个，所以只需要一个tick
	tick func()
	step stepFunc

	logger Logger
}

type Config struct {
	ID uint64

	Storage Storage

	Applied uint64

	// 每次拉取已提交但未应用日志的最大体积。防止状态机一次apply太多日志而出现问题
	MaxCommittedSizePerReady uint64

	// 每条消息最大字节数，0表示最多一条消息
	MaxSizePerMsg uint64

	// 未提交的日志最大字节数，0表示没有限制。超出这一限制，则会出现错误
	MaxUncommittedEntriesSize uint64

	// 最大已发送但未收到相应的日志个数。上层模块通常通过TCP/UDP发送日志，设置
	// MaxInflightMags防止将发送的内存池打满
	MaxInflightMsgs int

	// 集群伙伴的ID集合，包括自己
	// 只有开启一个新raft集群时，peers存在。否则panic
	peers []uint64

	// 所有learners的ID集合，包括自己
	// learner只接受leader的日志消息，不参与投票与竞选
	learners []uint64

	ElectionTick int
	HeartbeatTick int

	CheckQuorom bool
	PreVote bool

	// 只读请求的相关配置
	ReadOnlyOption ReadOnlyOption

	Logger Logger

	DisableProposalForwarding bool
}

func (c *Config) validate() error {
	if c.ID == None {
		return errors.New("cannot use none as id")
	}

	if c.HeartbeatTick <= 0 {
		return errors.New("heartbeat tick must be greater than 0")
	}

	if c.ElectionTick <= c.HeartbeatTick {
		return errors.New("election tick must be greater than heartbeat tick")
	}

	if c.Storage == nil {
		return errors.New("storage cannot be nil")
	}

	if c.MaxUncommittedEntriesSize == 0 {
		c.MaxUncommittedEntriesSize = noLimit
	}

	if c.MaxCommittedSizePerReady == 0 {
		c.MaxCommittedSizePerReady = c.MaxSizePerMsg
	}

	if c.Logger == nil {
		c.Logger = raftLogger
	}

	if c.ReadOnlyOption == ReadOnlyLeaseBased && !c.CheckQuorom {
		return errors.New("CheckQuorum must be enabled when ReadOnlyOption is ReadOnlyLeaseBased")
	}

	return nil
}

func newRaft(c *Config) *raft {
	if err := c.validate(); err != nil {
		panic(err.Error())
	}
	raftlog := newLogWithSize(c.Storage, c.Logger, c.MaxCommittedSizePerReady)
	// 通过配置（即初始化数据）,拿到节点基本信息、集群的伙伴信息。
	hs, cs, err := c.Storage.InitialState()
	if err != nil {
		panic(err)
	}
	peers := c.peers
	learners := c.learners

	if len(cs.Nodes) > 0 || len(cs.Learners) > 0 {
		if len(peers) > 0 || len(learners) > 0 {
			panic("cannot specify both newRaft(peers, learners) and ConfState.(Nodes, Learners)")
		}
		peers = cs.Nodes
		learners = cs.Learners
	}
	r := &raft{
		id: c.ID,
		lead: None,
		isLearner: false,
		raftLog: raftlog,
		maxMsgSize: c.MaxSizePerMsg,
		maxUncommittedSize: c.MaxUncommittedEntriesSize,
		prs: tracker.MakeProgressTracker(c.MaxInflightMsgs),
		electionTimeout: c.ElectionTick,
		heartbeatTimeout: c.HeartbeatTick,
		logger: c.Logger,
		checkQuorum: c.CheckQuorom,
		preVote: c.PreVote,
		readOnly: newReadOnly(c.ReadOnlyOption),
		disableProposalForwarding: c.DisableProposalForwarding,
	}
	for _, p := range peers {
		r.prs.InitProgress(p, 0, 1, false)
	}
	for _, p := range learners {
		r.prs.InitProgress(p, 0, 1, true)

		if r.id == p {
			r.isLearner = true
		}
	}

	if !isHardStateEqual(hs, emptyState) {
		r.loadState(hs)
	}
	if c.Applied > 0 {
		raftlog.appliedTo(c.Applied)
	}
	r.becomeFollower(r.Term, None)

	var nodesStrs []string
	for _, n := range r.prs.VoterNodes() {
		nodesStrs = append(nodesStrs, fmt.Sprintf("%x", n))
	}

	// peers 中不包含 learners
	r.logger.Infof("newRaft %x [peers: [%s], term: %d, commit: %d, applied: %d, lastindex: %d, lastterm: %d]",
		r.id, strings.Join(nodesStrs, ","), r.Term, r.raftLog.committed, r.raftLog.applied, r.raftLog.lastIndex(), r.raftLog.lastTerm())
	return r
}

func (r *raft) hasLeader() bool { return r.lead != None }

func (r *raft) loadState(state HardState) {
	if state.Commit < r.raftLog.committed || state.Commit > r.raftLog.lastIndex() {
		r.logger.Panicf("%x state.commit %d is out of range [%d, %d]", r.id, state.Commit, r.raftLog.committed, r.raftLog.lastIndex())
	}
	r.raftLog.committed = state.Commit
	r.Term = state.Term
	r.Vote = state.Vote
}

func (r *raft) becomeFollower(term uint64, lead uint64) {
	r.step = stepFollower
	r.reset(term)
	r.tick = r.tickElection
	r.lead = lead
	r.state = StateFollower
	r.logger.Infof("%x become follower at term %d", r.id, r.Term)
}

func (r *raft) becomePreCandidate() {
	if r.state == StateLeader {
		panic("invalid transition [leader -> pre-candidate]")
	}
	// stepCandidate() 封装了关于pre-candidate的方法
	r.step = stepCandidate
	r.prs.ResetVotes()
	r.tick = r.tickElection
	r.lead = None
	r.state = StatePreCandidate
	r.logger.Infof("%x become pre-candidate at term %d", r.id, r.Term)
}

func (r *raft) becomeCandidate() {
	if r.state == StateLeader {
		panic("invalid transition [leader -> candidate]")
	}
	r.step = stepCandidate
	// 只有成为candidate，term才会+1
	r.reset(r.Term + 1)
	r.tick = r.tickElection
	// 成为candidate时，将选票投给自己
	r.Vote = r.id
	r.state = StateCandidate
	r.logger.Infof("%x became candidate at term %d", r.id, r.Term)
}

func (r *raft) becomeLeader() {
	if r.state == StateFollower {
		panic("invalid transition [follower -> leader]")
	}
	r.step = stepCandidate
	// 成为Leader也要reset
	r.reset(r.Term)
	r.tick = r.tickHeartbeat
	r.lead = r.id
	r.state = StateLeader

	r.prs.Progress[r.id].BecomeReplicate()

	r.pendingConfIndex = r.raftLog.lastIndex()

	emptyEnt := Entry{Data: nil}
	if !r.appendEntry(emptyEnt) {
		r.logger.Panic("empty entry was dropped")
	}
	r.reduceUncommittedSize([]Entry{emptyEnt})
	r.logger.Infof("%x became leader at term %d", r.id, r.Term)
}

// 进入新一任期，所有投票信息、超时信息都要更新
func (r *raft) reset(term uint64) {
	if r.Term != term {
		r.Term = term
		r.Vote = None
	}
	r.lead = None

	r.electionElapsed = 0
	r.heartbeatElapsed = 0
	r.resetRandomizedElectionTimeout()

	r.abortLeaderTransfer()

	r.prs.ResetVotes()
	// 将每一个Progress的matchIndex设置为0，nextIndex设置为lastIndex
	r.prs.Visit(func(id uint64, pr *tracker.Progress) {
		*pr = tracker.Progress{
			Match: 0,
			Next: r.raftLog.lastIndex() + 1,
			Inflights: tracker.NewInflights(r.prs.MaxInflight),
			IsLearner: pr.IsLearner,
		}
		if id == r.id {
			pr.Match = r.raftLog.lastIndex()
		}
	})

	r.pendingConfIndex = 0
	r.uncommittedSize = 0
	r.readOnly = newReadOnly(r.readOnly.option)
}

func (r *raft) resetRandomizedElectionTimeout() {
	r.randomizedElectionTimeout = r.electionTimeout + globalRand.Intn(r.electionTimeout)
}

func (r *raft) abortLeaderTransfer() {
	r.leadTransferee = None
}

type stepFunc func(r *raft, m Message) error

// Follower收到不同类型消息后的行为
func stepFollower(r *raft, m Message) error {
	switch m.Type {
	case MsgApp:
		r.electionElapsed = 0
		r.lead = m.From
		r.handleAppendEntries(m)
	case MsgHeartbeat:
		r.electionElapsed = 0
		r.lead = m.From

	}
	return nil
}

func stepCandidate(r *raft, m Message) error {
	var myVoteRespType MessageType
	if r.state == StatePreCandidate {
		myVoteRespType = MsgPreVoteResp
	} else {
		myVoteRespType = MsgVoteResp
	}

	// 能够处理的消息类型
	switch m.Type {
	// 其他类型
	case myVoteRespType:
		// 统计投票
		gr, rj, res := r.poll(m.From, m.Type, !m.Reject)
		r.logger.Infof("%x has received %d %s votes and %d vote rejections", r.id, gr, m.Type, rj)
		switch res {
		case quorum.VoteWon:
			// 选举成功
			if r.state == StatePreCandidate {
				r.campaign(campaignElection)
			} else {
				r.becomeLeader()
				r.bcastAppend() // 向其他节点广播消息
			}
		case quorum.VoteLost:
			// 落选
			// pb.MsgPreVoteResp contains future term of pre-candidate
			// m.Term > r.Term; reuse r.Term
			r.becomeFollower(r.Term, None)
		}
	}
	return nil
}

func stepLeader(r *raft, m Message) error {
	// 这些消息不需要获取progress
	switch m.Type {
	case MsgBeat:
		r.bcastHeartbeat()
		return nil
	case MsgCheckQuorum:
		if pr := r.prs.Progress[r.id]; pr != nil {
			pr.RecentActive = true
		}
		if !r.prs.QuorunActive() {
			r.logger.Warningf("%x stepped down to follower since quorum is not active", r.id)
			r.becomeFollower(r.Term, None)
		}
		// 确认之后，将所有节点的RecentActive设置为false
		// 之后在心态的过程中会重新置回true
		r.prs.Visit(func(id uint64, pr *tracker.Progress) {
			if id != r.id {
				pr.RecentActive = false
			}
		})
		return nil
	case MsgProp:
		if len(m.Entries) == 0 {
			r.logger.Panicf("%x stepped empty MsgProp", r.id)
		}
		if r.prs.Progress[r.id] == nil {
			return ErrProposalDropped
		}

	}

	pr := r.prs.Progress[m.From]
	if pr == nil {
		r.logger.Debugf("%x no progress available for %x", r.id, m.From)
		return nil
	}
	switch m.Type {
	case MsgAppResp:
		pr.RecentActive = true

		if m.Reject {
			r.logger.Debugf("%x received msgApp rejection(lastindex: %d) from %x for index %d",
				r.id, m.RejectHint, m.From, m.Index)
			// 消息被拒绝，则leader更新对应节点的nextIndex
			if pr.MaybeDecrTo(m.Index, m.RejectHint) {
				r.logger.Debugf("%x decreased progress of %x to [%s]", r.id, m.From, pr)
				if pr.State == tracker.StateReplicate {
					pr.BecomeProbe()
				}
				// 这里主动又发送了一次消息，但这次消息的目的只是为了将pr.probeSent改为true(不可发送消息)
				// 这里也可以不主动调用，那么会在Leader下次调用sendAppend（）时起到同样作用
				r.sendAppend(m.From)
			}
		} else {
			// 消息已被Follower成功追加
			oldPaused := pr.IsPaused()
			if pr.MaybeUpdate(m.Index) { //更新对应pr的match和next
				switch {
				case pr.State == tracker.StateProbe:
					pr.BecomeReplicate()
				case pr.State == tracker.StateSnapshot && pr.Match >= pr.PendingSnapshot:
					r.logger.Debugf("%x recovered from needing snapshot, resumed sending replication messages to %x [%s]", r.id, m.From, pr)
					// Transition back to replicating state via probing state
					// (which takes the snapshot into account). If we didn't
					// move to replicating state, that would only happen with
					// the next round of appends (but there may not be a next
					// round for a while, exposing an inconsistent RaftStatus).
					pr.BecomeProbe()
					pr.BecomeReplicate()
				case pr.State == tracker.StateReplicate:
					pr.Inflights.FreeLE(m.Index)
				}

				// 只有达到半数以上回复后才commit，所以maybeCommit()
				if r.maybeCommit() {
					// 开始提交
					r.bcastAppend()
				} else if oldPaused {
					// If we were paused before, this node may be missing the
					// latest commit index, so send it.
					r.sendAppend(m.From)
				}

				// Leader迁移

			}
		}
	case MsgHeartbeatResp:
		pr.RecentActive = true
		pr.ProbeSent = false

		// P82
	}
	return nil
}

// 处理AppendEntries
func (r *raft) handleAppendEntries(m Message) {
	if m.Index < r.raftLog.committed {
		r.send(Message{To: m.From, Type: MsgAppResp, Index: r.raftLog.committed})
		return
	}

	if mlastIndex, ok := r.raftLog.maybeAppend(m.Index, m.LogTerm, m.Commit, m.Entries...); ok {
		r.send(Message{To: m.From, Type: MsgAppResp, Index: mlastIndex})
	} else {
		r.logger.Debugf("%x [logterm: %d, index: %d] rejected msgApp [logterm: %d, index: %d] from %x",
			r.id, r.raftLog.zeroTermOnErrCompacted(r.raftLog.term(m.Index)), m.Index, m.LogTerm, m.Index, m.From)
		r.send(Message{To: m.From, Type: MsgAppResp, Index: m.Index, Reject: true, RejectHint: r.raftLog.lastIndex()})
	}
}

func (r *raft) sendHeartbeat(to uint64, ctx []byte) {
	commit := min(r.prs.Progress[to].Match, r.raftLog.committed)
	m := Message{
		To: to,
		Type: MsgHeartbeat,
		Commit: commit,
		Context: ctx,
	}

	r.send(m)
}

// 成为leader后，向全体节点同步数据,全部按leader当前commited，各节点进行自身commit
func (r *raft) bcastAppend() {
	r.prs.Visit(func(id uint64, _ *tracker.Progress) {
		if id == r.id {
			return
		}

		r.sendAppend(id)
	})
}

// 广播心跳
func (r *raft) bcastHeartbeat() {
	lastCtx := r.readOnly.lastPendingRequestCtx()
	if len(lastCtx) == 0 {
		r.bcastHeartbeatWithCtx(nil)
	} else {
		r.bcastHeartbeatWithCtx([]byte(lastCtx))
	}
}

func (r *raft) bcastHeartbeatWithCtx(ctx []byte) {
	r.prs.Visit(func(id uint64, _ *tracker.Progress) {
		if id == r.id {
			return
		}
		r.sendHeartbeat(id, ctx)
	})

}

// sendAppend sends an append RPC with new entries (if any) and the
// current commit index to the given peer.
func (r *raft) sendAppend(to uint64) {
	r.maybeSendAppend(to, true)
}

// 根据progress中存储的节点matchIndex、nextIndex。向节点发送MsgApp消息或MsgSnapshot消息
//
// maybeSendAppend sends an append RPC with new entries to the given peer,
// if necessary. Returns true if a message was sent. The sendIfEmpty
// argument controls whether messages with no entries will be sent
// ("empty" messages are useful to convey updated Commit indexes, but
// are undesirable when we're sending multiple messages in a batch).
func (r *raft) maybeSendAppend(to uint64, sendIfEmpty bool) bool {
	pr := r.prs.Progress[to]
	if pr.IsPaused() {
		return false
	}
	m := Message{}
	m.To = to

	// 根据progress的nextIndex查询raftlog。若没有查到，说明该节点数据异常，发送snapshot
	term, errt := r.raftLog.term(pr.Next - 1)
	ents, erre := r.raftLog.entries(pr.Next, r.maxMsgSize)
	if len(ents) == 0 && !sendIfEmpty {
		return false
	}

	if errt != nil || erre != nil {
		// 在自身raftLog中找不到对应数据。发送snapshot给to
		if !pr.RecentActive {
			r.logger.Debugf("ignore sending snapshot to %x since it is not recently active", to)
			return false
		}

		m.Type = MsgSnap
		snapshot, err := r.raftLog.snapshot()
		if err != nil {
			if err == ErrSnapshotTemporarilyUnavailable {
				r.logger.Debugf("%x failed to send snapshot to %x because snapshot is temporarily unavailable", r.id, to)
				return false
			}
			panic(err)
		}
		if IsEmptySnap(snapshot) {
			panic("need non-empty snapshot")
		}
		m.Snapshot = snapshot
		sindex, sterm := snapshot.Metadata.Index, snapshot.Metadata.Term
		r.logger.Debugf("%x [firstindex: %d, commit: %d] sent snapshot[index: %d, term: %d] to %x [%s]",
			r.id, r.raftLog.firstIndex(), r.raftLog.committed, sindex, sterm, to, pr)
		pr.BecomeSnapshot(sindex)
		r.logger.Debugf("%x paused sending replication messages to %x [%s]", r.id, to, pr)

	} else {
		// 在自身raftlog中查找到数据，准备发给to
		m.Type = MsgApp
		m.Index = pr.Next - 1
		m.LogTerm = term
		m.Entries = ents
		m.Commit = r.raftLog.committed
		if n := len(m.Entries); n != 0 {
			switch pr.State {
			// optimistically increase the next when in StateReplicate
			case tracker.StateReplicate:
				last := m.Entries[n-1].Index
				// 更新该节点对应的nextIndex
				// 名字很形象，乐观的认为一定成功，所以在还未收到ack就将pr.next++
				// 因为向StateReplicate发送message是async，所以这里不等待回复就update。
				// 而且这里只更新nextIndex值，matchIndex需要等待回复才更新
				pr.OptimisticUpdate(last)
				pr.Inflights.Add(last)
			case tracker.StateProbe:
				pr.ProbeSent = true
			default:
				r.logger.Panicf("%x is sending append in unhandled state %s", r.id, pr.State)
			}
		}

	}
	r.send(m)
	return true
}

func (r *raft) appendEntry(es ...Entry) (accepted bool) {
	li := r.raftLog.lastIndex()
	for i := range es {
		es[i].Term = r.Term
		es[i].Index = li + 1 + uint64(i)
	}

	if !r.increaseUncommittedSize(es){
		r.logger.Debugf(
			"%x appending new entries to log would exceed uncommitted entry size limit; dropping proposal",
			r.id,
		)
		return false
	}

	li = r.raftLog.append(es...)
	// 更新当前节点对应Progress，主要是更新next和match
	r.prs.Progress[r.id].MaybeUpdate(li)
	r.maybeCommit()
	return true
}

func (r *raft) maybeCommit() bool {
	return true
}

func (r *raft) increaseUncommittedSize(ents []Entry) bool {
	var s uint64
	for _, e := range ents {
		s += uint64(PayloadSize(e))
	}

	if r.uncommittedSize > 0 && r.uncommittedSize+s > r.maxUncommittedSize {
		return false
	}
	r.uncommittedSize += s
	return true
}


// tickElection 选举时间推移
func (r *raft) tickElection() {
	r.electionElapsed++

	if r.promotable() && r.pastElectionTimeout() {
		r.electionElapsed = 0
		r.Step(Message{From: r.id, Type: MsgHup})
	}
}

// tickHeartbeat 心跳时间推移
func (r *raft) tickHeartbeat() {
	r.heartbeatElapsed++
	// leader通过electionTimeout触发CheckQuorum
	r.electionElapsed++

	if r.electionElapsed >= r.electionTimeout {
		r.electionElapsed = 0
		if r.checkQuorum {
			r.Step(Message{From: r.id, Type: MsgCheckQuorum})
		}
		// If current leader cannot transfer leadership in electionTimeout, it becomes leader again.
		// leader好像可以将自己的leader转移给其他节点
		if r.state == StateLeader && r.leadTransferee != None {
			r.abortLeaderTransfer()
		}
	}

	if r.state != StateLeader {
		return
	}

	if r.heartbeatElapsed >= r.heartbeatTimeout {
		r.heartbeatElapsed = 0
		r.Step(Message{From: r.id, Type: MsgBeat})
	}
}

func (r *raft) handleHeartbeat(m Message) {
	r.raftLog.commitTo(m.Commit)
	r.send(Message{To: m.From, Type: MsgHeartbeatResp, Context: m.Context})
}

// 超过选举时间
func (r *raft) pastElectionTimeout() bool {
	return r.electionElapsed >= r.randomizedElectionTimeout
}

// 是否能成为leader（learner永远不能成为leader）
func (r *raft) promotable() bool {
	// 得到raft自身在Progress中的身份
	pr := r.prs.Progress[r.id]
	return pr != nil && !pr.IsLearner
}

type lockedRand struct {
	mu sync.Mutex
	rand *rand.Rand
}

func (r *lockedRand) Intn(n int) int {
	r.mu.Lock()
	v := r.rand.Intn(n)
	r.mu.Unlock()
	return v
}

// TODO 为什么Rand需要加锁
var globalRand = &lockedRand{
	rand: rand.New(rand.NewSource(time.Now().UnixNano())),
}

// 统计选票
// 每一条信息来了都会调用poll方法
// RecordVote是赋值。给序号为·id·的节点赋值投票结果·v·
// TallyVotes是统计投票结果
func (r *raft) poll(id uint64, t MessageType, v bool) (granted int, rejected int, result quorum.VoteResult) {
	if v {
		r.logger.Infof("%x received %s from %x at term %d", r.id, t, id, r.Term)
	} else {
		r.logger.Infof("%x received %s rejection from %x at term %d", r.id, t, id, r.Term)
	}
	r.prs.RecordVote(id, v)
	return r.prs.TallyVotes()
}

// 通过一条Message决定要做的事
// 处理各类消息的入口
func (r *raft) Step(m Message) error {
	switch {   // 根据term值进行分类处理
	case m.Term == 0:
		// local message
	case m.Term > r.Term:
		if m.Type == MsgVote || m.Type == MsgPreVote {
			// 根据消息的Context字段判断收到的消息是否为
			// Leader节点转移场景下产生的，如果是，则强制当前节点参与本次预选
			force := bytes.Equal(m.Context, []byte(campaignTransfer))
			// 通过一系列条件判断，判断当前节点是否参与选举
			// TODO 为什么判断这么多条件
			inLease := r.checkQuorum && r.lead != None && r.electionElapsed < r.electionTimeout
			if !force && inLease {
				// If a server receives a RequestVote request within the minimum election timeout
				// of hearing from a current leader, it does not update its term or grant its vote
				r.logger.Infof("%x [logterm: %d, index: %d, vote: %x] ignored %s from %x [logterm: %d, index: %d] at term %d: lease is not expired (remaining ticks: %d)",
					r.id, r.raftLog.lastTerm(), r.raftLog.lastIndex(), r.Vote, m.Type, m.From, m.LogTerm, m.Index, r.Term, r.electionTimeout-r.electionElapsed)
				// 不参与选举
				return nil
			}
		}
		switch {
		case m.Type == MsgPreVote:
			// Never change our term in response to a PreVote
		}
	}

	switch m.Type {  // 根据消息类型进行分类处理
	case MsgHup:
		if r.state != StateLeader { //只有非leader才会处理MsgHup
			if !r.promotable() {
				r.logger.Warningf("%x is unpromotable and can not campaign; ignoring MsgHup", r.id)
			}
			// 获取已提交但未应用的日志
			ents, err := r.raftLog.slice(r.raftLog.applied+1, r.raftLog.committed+1, noLimit)
			if err != nil {
				r.logger.Panicf("unexpected error getting unapplied entries (%v)", err)
			}
			// 检测是否有未应用的EntryConfChange记录，如果有就放弃发起选举的机会
			if n := numOfPendingConf(ents); n != 0 && r.raftLog.committed > r.raftLog.applied {
				r.logger.Warningf("%x cannot campaign at term %d since there are still %d pending configuration changes to apply", r.id, r.Term, n)
				return nil
			}

			r.logger.Infof("%x is starting a new election at term %d", r.id, r.Term)
			if r.preVote {
				r.campaign(campaignPreElection)
			} else {
				r.campaign(campaignElection)
			}
		} else {
			r.logger.Debugf("%x ignoring MsgHup because already leader", r.id)
		}

	case MsgVote, MsgPreVote:
		// 当前节点在参与选举时，会综合以下几个条件决定是否投票
		// 1、当前节点是否已经投过票了
		// 2、MsgPreVote、MsgVote消息发送者的任期号是否更大
		// 3、当前节点投票给了对方节点
		// 4、MsgPreVote、MsgVote消息发送者的raftLog中是否包含了当前节点的全部Entry记录（isUpToDate）

		// learner不参与投票
		if r.isLearner {
			r.logger.Infof("%x [logterm: %d, index: %d, vote: %x] ignored %s from %x [logterm: %d, index: %d] at term %d: learner can not vote",
				r.id, r.raftLog.lastTerm(), r.raftLog.lastIndex(), r.Vote, m.Type, m.From, m.LogTerm, m.Index, r.Term)
			return nil
		}

		canVote := r.Vote == m.From || (r.Vote == None && r.lead == None) || (m.Type == MsgPreVote && m.Term > r.Term)
		if canVote && r.raftLog.isUpToDate(m.Index, m.LogTerm) {
			// 投票
			r.logger.Infof("%x [logterm: %d, index: %d, vote: %x] cast %s for %x [logterm: %d, index: %d] at term %d",
				r.id, r.raftLog.lastTerm(), r.raftLog.lastIndex(), r.Vote, m.Type, m.From, m.LogTerm, m.Index, r.Term)
			r.send(Message{To: m.From, Term: m.Term, Type: voteRespMsgType(m.Type)})
			if m.Type == MsgVote {
				// 将自身vote赋值
				r.electionElapsed = 0
				r.Vote = m.From
			}
		} else {
			r.logger.Infof("%x [logterm: %d, index: %d, vote: %x] rejected %s from %x [logterm: %d, index: %d] at term %d",
				r.id, r.raftLog.lastTerm(), r.raftLog.lastIndex(), r.Vote, m.Type, m.From, m.LogTerm, m.Index, r.Term)
			r.send(Message{To: m.From, Term: r.Term, Type: voteRespMsgType(m.Type), Reject: true})
		}

	default:
		err := r.step(r, m)
		if err != nil {
			return err
		}
	}
	return nil
}

type CampaignType string

// Possible values for CampaignType
// 竞选只有三种可能性：1：竞选preLeader 2：竞选leader 3：leader要转让leader
const (
	// campaignPreElection represents the first phase of a normal election when
	// Config.PreVote is true.
	campaignPreElection CampaignType = "CampaignPreElection"
	// campaignElection represents a normal (time-based) election (the second phase
	// of the election when Config.PreVote is true).
	campaignElection CampaignType = "CampaignElection"
	// campaignTransfer represents the type of leader transfer
	campaignTransfer CampaignType = "CampaignTransfer"
)

func (r *raft) campaign(t CampaignType) {
	if !r.promotable() {
		// This path should not be hit (callers are supposed to check), but
		// better safe than sorry.
		r.logger.Warningf("%x is unpromotable; campaign() should have been called", r.id)
	}

	var term uint64
	var voteMsg MessageType
	if t == campaignPreElection {
		r.becomePreCandidate()
		voteMsg = MsgPreVote
		// term + 1是因为，成为preCandidate，自身term还没有+1。但要想其他节点通知的term需+1
		term = r.Term + 1
	} else {
		r.becomeCandidate()
		voteMsg = MsgVote
		term = r.Term
	}
	// 统计选票，这次检测主要是为了单点设置的
	if _, _, res := r.poll(r.id, voteRespMsgType(voteMsg), true); res == quorum.VoteWon {
		if t == campaignPreElection {
			r.campaign(campaignElection)
		} else {
			r.becomeLeader()
		}
		return
	}
	// 不是单点会走到这里。向集群中所有节点发送message
	for id := range r.prs.Voters.IDs() {
		if id == r.id {
			continue
		}
		r.logger.Infof("%x [logterm: %d, index: %d] sent %s request to %x at term %d",
			r.id, r.raftLog.lastTerm(), r.raftLog.lastIndex(), voteMsg, id, r.Term)

		var ctx []byte
		if t == campaignTransfer {
			ctx = []byte(t)
		}
		r.send(Message{Term: term, To: id, Type: voteMsg, Index: r.raftLog.lastIndex(), LogTerm: r.raftLog.lastTerm(), Context: ctx})
	}

}

func (r *raft) send(m Message) {
	m.From = r.id
	if m.Type == MsgVote || m.Type == MsgVoteResp || m.Type == MsgPreVote || m.Type == MsgPreVoteResp {
		// 这些消息的Term值不应该为0
		// 即所有关于竞选的消息类型term都不应该为0
		if m.Term == 0 {
			// All {pre-,}campaign messages need to have the term set when
			// sending.
			// - MsgVote: m.Term is the term the node is campaigning for,
			//   non-zero as we increment the term when campaigning.
			// - MsgVoteResp: m.Term is the new r.Term if the MsgVote was
			//   granted, non-zero for the same reason MsgVote is
			// - MsgPreVote: m.Term is the term the node will campaign,
			//   non-zero as we use m.Term to indicate the next term we'll be
			//   campaigning for
			// - MsgPreVoteResp: m.Term is the term received in the original
			//   MsgPreVote if the pre-vote was granted, non-zero for the
			//   same reasons MsgPreVote is
			panic(fmt.Sprintf("term should be set when sending %s", m.Type))
		}
	} else {
		// 这些消息的Term应该为0。在这一步term被赋值
		if m.Term != 0 {
			panic(fmt.Sprintf("term should not be set when sending %s (was %d)", m.Type, m.Term))
		}
		// do not attach term to MsgProp, MsgReadIndex
		// proposals are a way to forward to the leader and
		// should be treated as local message.
		// MsgReadIndex is also forwarded to leader.
		if m.Type != MsgProp && m.Type != MsgReadIndex {
			m.Term = r.Term
		}
	}
	r.msgs = append(r.msgs, m)
}

// reduceUncommittedSize accounts for the newly committed entries by decreasing
// the uncommitted entry size limit.
func (r *raft) reduceUncommittedSize(ents []Entry) {
	if r.uncommittedSize == 0 {
		// Fast-path for followers, who do not track or enforce the limit.
		return
	}

	var s uint64
	for _, e := range ents {
		s += uint64(PayloadSize(e))
	}
	if s > r.uncommittedSize {
		// uncommittedSize may underestimate the size of the uncommitted Raft
		// log tail but will never overestimate it. Saturate at 0 instead of
		// allowing overflow.
		r.uncommittedSize = 0
	} else {
		r.uncommittedSize -= s
	}
}

func numOfPendingConf(ents []Entry) int {
	n := 0
	for i := range ents {
		if ents[i].Type == EntryConfChange {
			n++
		}
	}
	return n
}