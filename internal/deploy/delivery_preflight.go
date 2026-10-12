package deploy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type deliveryPreflight struct {
	Hash    string `json:"hash"`
	Actor   string `json:"actor"`
	Nonce   string `json:"nonce"`
	Expires int64  `json:"expires"`
}

// Bind both the shown public plan and private target secrets. Encrypted Nacos
// backups use random nonces, so compare their stable MD5/diff/destination fields.
func deliveryPreflightHash(req DeliveryRequest, p *DeliveryPlan, secrets map[string]string) string {
	req.PreflightToken = ""
	req.previewDrafts = false
	copy := *p
	copy.PreflightToken = ""
	copy.PreflightExpires = 0
	copy.NacosPlans = publicNacosPlans(p.NacosPlans)
	return digestJSON([]any{req, copy, secrets})
}
func (s *Store) signDeliveryPreflight(req DeliveryRequest, p *DeliveryPlan, secrets map[string]string, actor string) (string, int64) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", 0
	}
	expiry := time.Now().Add(10 * time.Minute).Unix()
	raw, _ := json.Marshal(deliveryPreflight{deliveryPreflightHash(req, p, secrets), actor, hex.EncodeToString(random), expiry})
	signature := hmac.New(sha256.New, s.secretKey)
	signature.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(signature.Sum(nil)), expiry
}
func (s *Store) verifyDeliveryPreflight(req DeliveryRequest, p *DeliveryPlan, secrets map[string]string, actor string) (string, error) {
	fail := fmt.Errorf("预检已过期，或范围、制品、目标配置发生变化，请重新预检")
	parts := strings.Split(req.PreflightToken, ".")
	if len(parts) != 2 {
		return "", fail
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fail
	}
	sig, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", fail
	}
	mac := hmac.New(sha256.New, s.secretKey)
	mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", fail
	}
	var claim deliveryPreflight
	if json.Unmarshal(raw, &claim) != nil || claim.Actor != actor || claim.Expires < time.Now().Unix() || claim.Hash != deliveryPreflightHash(req, p, secrets) {
		return "", fail
	}
	return claim.Nonce, nil
}
