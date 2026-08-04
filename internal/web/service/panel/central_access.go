package panel

import (
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	"github.com/mhsanaei/3x-ui/v3/internal/util/random"
)

const centralAccessTokenLength = 48

// CentralAccessTokenView deliberately omits TokenHash. Token is returned only
// by Create and must be copied before the response is discarded.
type CentralAccessTokenView struct {
	Id         int    `json:"id"`
	Name       string `json:"name"`
	Token      string `json:"token,omitempty"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
	LastUsedIP string `json:"lastUsedIp"`
}

type CentralAccessService struct{}

func centralAccessTokenView(row *model.CentralAccessToken) *CentralAccessTokenView {
	return &CentralAccessTokenView{
		Id: row.Id, Name: row.Name, Enabled: row.Enabled, CreatedAt: row.CreatedAt,
		LastUsedAt: row.LastUsedAt, LastUsedIP: row.LastUsedIP,
	}
}

func (s *CentralAccessService) List() ([]*CentralAccessTokenView, error) {
	var rows []*model.CentralAccessToken
	if err := database.GetDB().Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*CentralAccessTokenView, 0, len(rows))
	for _, row := range rows {
		result = append(result, centralAccessTokenView(row))
	}
	return result, nil
}

func (s *CentralAccessService) Create(name string) (*CentralAccessTokenView, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, common.NewError("token name is required")
	}
	if len(name) > 64 {
		return nil, common.NewError("token name must be 64 characters or fewer")
	}
	var count int64
	db := database.GetDB()
	if err := db.Model(&model.CentralAccessToken{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, common.NewError("a central access token with that name already exists")
	}
	plaintext := random.Seq(centralAccessTokenLength)
	row := &model.CentralAccessToken{Name: name, TokenHash: crypto.HashTokenSHA256(plaintext), Enabled: true}
	if err := db.Create(row).Error; err != nil {
		return nil, err
	}
	view := centralAccessTokenView(row)
	view.Token = plaintext
	return view, nil
}

func (s *CentralAccessService) SetEnabled(id int, enabled bool) error {
	if id <= 0 {
		return common.NewError("invalid token id")
	}
	updates := map[string]any{"enabled": enabled}
	if !enabled {
		updates["revoked_at"] = time.Now().Unix()
	} else {
		updates["revoked_at"] = 0
	}
	result := database.GetDB().Model(&model.CentralAccessToken{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("token not found")
	}
	return nil
}

func (s *CentralAccessService) Delete(id int) error {
	if id <= 0 {
		return common.NewError("invalid token id")
	}
	result := database.GetDB().Where("id = ?", id).Delete(&model.CentralAccessToken{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("token not found")
	}
	return nil
}

// MatchAndRecord authenticates only the central read-only route group. The
// stored hash is constant-time compared and the last-use audit fields are
// updated at most once every five minutes per token.
func (s *CentralAccessService) MatchAndRecord(presented, remoteIP string) bool {
	if presented == "" {
		return false
	}
	var rows []*model.CentralAccessToken
	if err := database.GetDB().Where("enabled = ?", true).Find(&rows).Error; err != nil {
		return false
	}
	presentedHash := []byte(crypto.HashTokenSHA256(presented))
	var matched *model.CentralAccessToken
	for _, row := range rows {
		if subtle.ConstantTimeCompare([]byte(row.TokenHash), presentedHash) == 1 {
			matched = row
		}
	}
	if matched == nil {
		return false
	}
	now := time.Now().Unix()
	if matched.LastUsedAt < now-300 || matched.LastUsedIP != remoteIP {
		_ = database.GetDB().Model(&model.CentralAccessToken{}).Where("id = ? AND enabled = ?", matched.Id, true).
			Updates(map[string]any{"last_used_at": now, "last_used_ip": remoteIP}).Error
	}
	return true
}
