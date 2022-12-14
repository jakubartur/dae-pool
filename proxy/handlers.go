package proxy

import (
	"log"
	"regexp"
	"strings"

	"github.com/daefrom/dae-pool/rpc"
	"github.com/daefrom/dae-pool/util"
)

// Allow only lowercase hexadecimal with 0x prefix
var noncePattern = regexp.MustCompile("^0x[0-9a-f]{16}$")
var hashPattern = regexp.MustCompile("^0x[0-9a-f]{64}$")
var workerPattern = regexp.MustCompile("^[0-9a-zA-Z-_]{1,8}$")

// Stratum
func (s *ProxyServer) handleLoginRPC(cs *Session, params []string, id string) (bool, *ErrorReply) {
	var loginCheck string
	if len(params) == 0 {
		return false, &ErrorReply{Code: -1, Message: "Invalid params"}
	}

	//Parse email Id here
	//TODO: LOGIN CHECK OF VALID ID
	// log.Printf("Stratum miner params %s => %v", params, len(params))
	if len(params) > 2 {
		return false, &ErrorReply{Code: -1, Message: "Invalid params"}
	}

	login := strings.ToLower(params[0])
	if strings.Contains(login, ".") {
		longString := strings.Split(login, ".")
		loginCheck = longString[0]
	} else {
		loginCheck = login
	}
	//log.Printf("Check params from %s@%s %v", login, cs.ip, params)
	if !util.IsValidHexAddress(loginCheck) {
		return false, &ErrorReply{Code: -1, Message: "Invalid login"}
	}

	if !s.policy.ApplyLoginPolicy(login, cs.ip) {
		return false, &ErrorReply{Code: -1, Message: "You are blacklisted"}
	}

	if len(params) > 1 {
		s.backend.WritePasswordByMiner(login, params[1])
	}

	cs.login = login
	s.registerSession(cs)
	//log.Printf("Stratum miner connected %v@%v", login, cs.ip)
	return true, nil
}

func (s *ProxyServer) handleGetWorkRPC(cs *Session) ([]string, *ErrorReply) {
	t := s.currentBlockTemplate()
	if t == nil || len(t.Header) == 0 || s.isSick() {
		return nil, &ErrorReply{Code: 0, Message: "Work not ready"}
	}
	return []string{t.Header, t.Seed, s.diff, util.ToHex(int64(t.Height))}, nil
}

// Stratum
func (s *ProxyServer) handleTCPSubmitRPC(cs *Session, id string, params []string) (bool, *ErrorReply) {
	s.sessionsMu.RLock()
	_, ok := s.sessions[cs]
	s.sessionsMu.RUnlock()

	if !ok {
		return false, &ErrorReply{Code: 25, Message: "Not subscribed"}
	}
	return s.handleSubmitRPC(cs, cs.login, id, params)
}

func (s *ProxyServer) handleSubmitRPC(cs *Session, login, id string, params []string) (bool, *ErrorReply) {
	if strings.Contains(login, ".") {
		longString := strings.Split(login, ".")
		id = longString[1]
		login = longString[0]
	}

	if !workerPattern.MatchString(id) {
		id = "daeMiner"
	}
	if len(params) != 3 {
		s.policy.ApplyMalformedPolicy(cs.ip)
		log.Printf("Malformed params from %s@%s %v", login, cs.ip, params)
		return false, &ErrorReply{Code: -1, Message: "Invalid params"}
	}

	stratumMode := cs.stratumMode()
	if stratumMode == NiceHash {
		for i := 0; i <= 2; i++ {
			if params[i][0:2] != "0x" {
				params[i] = "0x" + params[i]
			}
		}
	}

	if !noncePattern.MatchString(params[0]) || !hashPattern.MatchString(params[1]) || !hashPattern.MatchString(params[2]) {
		s.policy.ApplyMalformedPolicy(cs.ip)
		log.Printf("Malformed PoW result from %s@%s %v", login, cs.ip, params)
		return false, &ErrorReply{Code: -1, Message: "Malformed PoW result"}
	}
	t := s.currentBlockTemplate()
	exist, validShare := s.processShare(login, id, cs.ip, t, params)
	ok := s.policy.ApplySharePolicy(cs.ip, !exist && validShare)

	//log.Printf("Valid share from %s@%s", login, cs.ip)

	if exist {
		log.Printf("Duplicate share from %s@%s %v", login, cs.ip, params)
		// see https://github.com/sammy007/open-ethereum-pool/compare/master...nicehashdev:patch-1
		if !ok {
			return false, &ErrorReply{Code: 23, Message: "Invalid share"}
		}
		return false, nil
	}

	if !validShare {
		//	log.Printf("Invalid share from %s@%s", login, cs.ip)
		// Bad shares limit reached, return error and close
		if !ok {
			return false, &ErrorReply{Code: 23, Message: "Invalid share"}
		}
		return false, nil
	}
	if s.config.Proxy.Debug {
		//	log.Printf("Valid share from %s@%s", login, cs.ip)
	}

	if !ok {
		return true, &ErrorReply{Code: -1, Message: "High rate of invalid shares"}
	}
	return true, nil
}

func (s *ProxyServer) handleGetBlockByNumberRPC() *rpc.GetBlockReplyPart {
	t := s.currentBlockTemplate()
	var reply *rpc.GetBlockReplyPart
	if t != nil {
		reply = t.GetPendingBlockCache
	}
	return reply
}

func (s *ProxyServer) handleUnknownRPC(cs *Session, m string) *ErrorReply {
	log.Printf("Unknown request method %s from %s", m, cs.ip)
	s.policy.ApplyMalformedPolicy(cs.ip)
	return &ErrorReply{Code: -3, Message: "Method not found"}
}
