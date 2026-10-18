// Package region says where in Japan a user is watching: one prefecture, or the whole country.
package region

import (
	"strconv"
	"strings"
)

const (
	prefectureCount = 47
	maxCodeDigits   = 2
)

// Prefecture is one of the 47, with its JIS code.
type Prefecture struct {
	Code     int
	English  string
	Japanese string
}

// Prefectures lists all 47 in JIS code order. It returns a fresh slice so no caller can change the list for others.
func Prefectures() []Prefecture {
	return []Prefecture{
		{1, "Hokkaido", "北海道"},
		{2, "Aomori", "青森県"},
		{3, "Iwate", "岩手県"},
		{4, "Miyagi", "宮城県"},
		{5, "Akita", "秋田県"},
		{6, "Yamagata", "山形県"},
		{7, "Fukushima", "福島県"},
		{8, "Ibaraki", "茨城県"},
		{9, "Tochigi", "栃木県"},
		{10, "Gunma", "群馬県"},
		{11, "Saitama", "埼玉県"},
		{12, "Chiba", "千葉県"},
		{13, "Tokyo", "東京都"},
		{14, "Kanagawa", "神奈川県"},
		{15, "Niigata", "新潟県"},
		{16, "Toyama", "富山県"},
		{17, "Ishikawa", "石川県"},
		{18, "Fukui", "福井県"},
		{19, "Yamanashi", "山梨県"},
		{20, "Nagano", "長野県"},
		{21, "Gifu", "岐阜県"},
		{22, "Shizuoka", "静岡県"},
		{23, "Aichi", "愛知県"},
		{24, "Mie", "三重県"},
		{25, "Shiga", "滋賀県"},
		{26, "Kyoto", "京都府"},
		{27, "Osaka", "大阪府"},
		{28, "Hyogo", "兵庫県"},
		{29, "Nara", "奈良県"},
		{30, "Wakayama", "和歌山県"},
		{31, "Tottori", "鳥取県"},
		{32, "Shimane", "島根県"},
		{33, "Okayama", "岡山県"},
		{34, "Hiroshima", "広島県"},
		{35, "Yamaguchi", "山口県"},
		{36, "Tokushima", "徳島県"},
		{37, "Kagawa", "香川県"},
		{38, "Ehime", "愛媛県"},
		{39, "Kochi", "高知県"},
		{40, "Fukuoka", "福岡県"},
		{41, "Saga", "佐賀県"},
		{42, "Nagasaki", "長崎県"},
		{43, "Kumamoto", "熊本県"},
		{44, "Oita", "大分県"},
		{45, "Miyazaki", "宮崎県"},
		{46, "Kagoshima", "鹿児島県"},
		{47, "Okinawa", "沖縄県"},
	}
}

// Lookup finds a prefecture by its English name (ignoring case), its full Japanese name, or its number from 1 to 47.
func Lookup(name string) (Prefecture, bool) {
	name = strings.TrimSpace(name)
	all := Prefectures()
	if code, ok := parseCode(name); ok {
		if code < 1 || code > len(all) {
			return Prefecture{}, false
		}
		return all[code-1], true
	}
	for _, p := range all {
		if strings.EqualFold(p.English, name) || p.Japanese == name {
			return p, true
		}
	}
	return Prefecture{}, false
}

// parseCode accepts one or two plain digits only. A number parser would also take "+13" and "1e1", and one plain
// spelling per prefecture keeps the README's table the whole truth.
func parseCode(s string) (int, bool) {
	if s == "" || len(s) > maxCodeDigits {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	code, err := strconv.Atoi(s)
	return code, err == nil
}

type kind int

// kindUnset is the zero value, so a Location nobody filled in cannot pass for "all Japan" and send every alert.
const (
	kindUnset kind = iota
	kindEverywhere
	kindPrefecture
)

// Location is where alerts matter to the user. It is comparable, so two locations can be checked with ==. The
// prefectures are a bit set, one bit per JIS code, which also makes order and repeats in a list irrelevant.
type Location struct {
	kind kind
	mask uint64
}

// Everywhere watches the whole country, for someone moving around.
func Everywhere() Location { return Location{kind: kindEverywhere} }

func InPrefecture(p Prefecture) Location { return InPrefectures(p) }

// InPrefectures watches every listed prefecture. A list with no valid prefecture gives the unset location rather than
// all Japan, so an empty setting can never send every alert.
func InPrefectures(list ...Prefecture) Location {
	var mask uint64
	for _, p := range list {
		if p.Code >= 1 && p.Code <= prefectureCount {
			mask |= bit(p.Code)
		}
	}
	if mask == 0 {
		return Location{}
	}
	return Location{kind: kindPrefecture, mask: mask}
}

func bit(code int) uint64 { return uint64(1) << (code - 1) }

func (l Location) IsEverywhere() bool { return l.kind == kindEverywhere }

// Covers says whether an alert for the prefecture with this JIS code matters here. Matching is by prefecture, not by
// distance.
func (l Location) Covers(code int) bool {
	switch l.kind {
	case kindEverywhere:
		return true
	case kindPrefecture:
		return code >= 1 && code <= prefectureCount && l.mask&bit(code) != 0
	case kindUnset:
		return false
	default:
		return false
	}
}

func (l Location) String() string {
	switch l.kind {
	case kindEverywhere:
		return "all Japan"
	case kindPrefecture:
		var names []string
		for _, p := range Prefectures() {
			if l.Covers(p.Code) {
				names = append(names, p.English)
			}
		}
		return strings.Join(names, ", ")
	case kindUnset:
		return "unset"
	default:
		return "unset"
	}
}
