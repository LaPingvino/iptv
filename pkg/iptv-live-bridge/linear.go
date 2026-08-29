package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type ScheduledSegment struct {
	Name         string
	Title        string
	Desc         string
	Category     string
	IsTransition bool
}

type LinearStation struct {
	dir         string
	prefix      string
	segDuration float64
	mu          sync.RWMutex
	lastScan    time.Time
	schedule    []ScheduledSegment
	discOffsets []int
}

var pasportoEpInfo = map[int][2]string{
	1:  {"Bonvenon al nia hejmo!", "Enkonduko al la familio Bonvolo kaj ilia gastiga hejmo."},
	2:  {"Kiu estas tiu?", "Novaj gastoj alvenas kaj misteraj situacioj komenciĝas."},
	3:  {"La perdita valizo", "Serĉado de perdita valizo kaj amuzaj miskomprenoj."},
	4:  {"Surprizo en la kuirejo", "Kuirartaj aventuroj kaj nekutimaj petoj."},
	5:  {"La granda festo", "Preparado por granda familia festo kun amikoj."},
	6:  {"Nekonata vizitanto", "Mistera vizitanto aperas ĉe la pordo."},
	7:  {"La sekreto malkaŝita", "Gravaj sekretoj kaj komediaj klarigoj."},
	8:  {"Vojaĝaj planoj", "La familio kaj gastoj planas novajn vojaĝojn tra la mondo."},
	9:  {"Trajnoj kaj biletoj", "Aventuroj ĉe la stacidomo."},
	10: {"Aventuro en la urbo", "Esplorado de nova urbo kaj renkontoj."},
	11: {"La hotelo", "Restado en hotelo kun neatenditaj surprizoj."},
	12: {"La restoracio", "Mendo de manĝaĵoj kaj lingvaj defioj."},
	13: {"Sur la strando", "Someraj ferioj kaj amuzaj agadoj ĉe la maro."},
	14: {"La muzeo", "Kultura vizito al muzeo kun historiaj sekretoj."},
	15: {"La adiaŭa vespero", "Gaja vespero antaŭ la reveno hejmen."},
	16: {"Ĝis revido, amikoj!", "La granda finalo de Pasporto al la Tuta Mondo."},
}

var senlimeEpInfo = map[int][2]string{
	1:  {"La unua serio en Esperanto", "La teamoj ekas sian grandan vojaĝon tra Eŭropo."},
	2:  {"Konstruado per dolĉaĵoj kaj spagetoj", "Krea defio uzanta nur dolĉaĵojn kaj spagetojn."},
	3:  {"Riskante la vivon sur du radoj", "Biciklaj defioj kaj rapidaj kuroj tra la urbo."},
	4:  {"Filmproduktado en trajnoj", "Kreado de filmetoj dum veturado per trajno."},
	5:  {"La venĝo de la trajnoj", "Fervojaj misaventuroj kaj horar-defioj."},
	6:  {"Rilaksado inter bestoj", "Vizito al bestoj kaj trankvilaj momentoj."},
	7:  {"ASMR kun arboj", "Nekutima kaj amuza natura ASMR-defio."},
	8:  {"Pluvo kaj suno", "Veteraj defioj dum la subĉiela vojaĝo."},
	9:  {"Vojaĝi sen biletoj", "Strategiaj vojaĝdefioj kaj amuzaj taskoj."},
	10: {"Mangirdito scias kion vi faris", "Misteraj ludoj kaj teamaj taktikoj."},
	11: {"Supren, suben kaj akven", "Akvo-defioj kaj sportaj agadoj."},
	12: {"Plaĝa tago", "Amuzaj ludoj kaj defioj ĉe la marbordo."},
	13: {"La voko de Ĥthuluzo", "Mistera vespera defio kun mitologia etoso."},
	14: {"Nia plej granda perdo", "Dramaj momentoj kaj poentaj ŝanĝoj."},
	15: {"Gajni plej gravas", "La granda antaŭ-finala konkurso."},
	16: {"Ni nur parolas Esperanton", "La grandioza finalo de Esperanto Senlime Sezono 1!"},
}

func NewLinearStation(dir, prefix string, segDuration float64) *LinearStation {
	ls := &LinearStation{
		dir:         dir,
		prefix:      strings.Trim(prefix, "/"),
		segDuration: segDuration,
	}
	ls.rebuildSchedule()
	return ls
}

func (ls *LinearStation) rebuildSchedule() {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	entries, err := os.ReadDir(ls.dir)
	if err != nil || len(entries) == 0 {
		ls.schedule = nil
		ls.discOffsets = nil
		return
	}

	var allSegs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ts") {
			allSegs = append(allSegs, e.Name())
		}
	}
	sort.Strings(allSegs)

	if strings.Contains(ls.prefix, "esperanto") {
		ls.buildEsperantoSchedule(allSegs)
	} else {
		ls.buildDefaultSchedule(allSegs)
	}

	// Precompute discontinuity indices
	var discs []int
	for i, item := range ls.schedule {
		if item.IsTransition {
			discs = append(discs, i)
		}
	}
	ls.discOffsets = discs
	ls.lastScan = time.Now()
}

func (ls *LinearStation) buildEsperantoSchedule(allSegs []string) {
	shows := make(map[string][]string)
	var bumperSegs []string

	for _, seg := range allSegs {
		if strings.HasPrefix(seg, "dok_estas_parto_01_") {
			bumperSegs = append(bumperSegs, seg)
		} else if strings.HasPrefix(seg, "ident_z_") {
			continue // Skip old buggy synthetic vignette
		} else {
			idx := strings.LastIndex(seg, "_")
			var key string
			if idx != -1 {
				key = seg[:idx]
			} else {
				key = "misc"
			}
			shows[key] = append(shows[key], seg)
		}
	}

	// Collect series
	var pasporto [][]string
	for i := 1; i <= 16; i++ {
		k := fmt.Sprintf("pasporto_%02d", i)
		if s, ok := shows[k]; ok && len(s) > 0 {
			pasporto = append(pasporto, s)
		}
	}

	var senlime [][]string
	for i := 1; i <= 16; i++ {
		k := fmt.Sprintf("senlime_s01e%02d", i)
		if s, ok := shows[k]; ok && len(s) > 0 {
			senlime = append(senlime, s)
		}
	}

	var mvKeys []string
	for k := range shows {
		if strings.HasPrefix(k, "mv_") {
			mvKeys = append(mvKeys, k)
		}
	}
	sort.Strings(mvKeys)

	var mvBlocks [][]string
	for i := 0; i < len(mvKeys); i += 2 {
		var b []string
		for j := i; j < i+2 && j < len(mvKeys); j++ {
			b = append(b, shows[mvKeys[j]]...)
		}
		if len(b) > 0 {
			mvBlocks = append(mvBlocks, b)
		}
	}

	var docKeys []string
	for k := range shows {
		if strings.HasPrefix(k, "dok_") {
			docKeys = append(docKeys, k)
		}
	}
	sort.Strings(docKeys)

	var specials [][]string
	for _, k := range docKeys {
		specials = append(specials, shows[k])
	}
	if mazi, ok := shows["mazi"]; ok && len(mazi) > 0 {
		chunkSize := 115
		for i := 0; i < len(mazi); i += chunkSize {
			end := i + chunkSize
			if end > len(mazi) {
				end = len(mazi)
			}
			specials = append(specials, mazi[i:end])
		}
	}

	maxRounds := len(pasporto)
	if len(senlime) > maxRounds {
		maxRounds = len(senlime)
	}
	if len(specials) > maxRounds {
		maxRounds = len(specials)
	}
	if len(mvBlocks) > maxRounds {
		maxRounds = len(mvBlocks)
	}

	type blockDef struct {
		title    string
		desc     string
		category string
		segs     []string
	}

	var programBlocks []blockDef

	bumperDef := blockDef{
		title:    "Esperanto Estas: Enkonduko",
		desc:     "Oficiala stacia vineto kaj enkonduko al la internacia lingvo Esperanto.",
		category: "Vineto",
		segs:     bumperSegs,
	}

	if maxRounds == 0 {
		// General fallback for unclassified shows
		for k, segs := range shows {
			programBlocks = append(programBlocks, blockDef{
				title:    strings.Title(strings.ReplaceAll(k, "_", " ")),
				desc:     fmt.Sprintf("Broadcast of %s", k),
				category: "General",
				segs:     segs,
			})
			if len(bumperSegs) > 0 {
				programBlocks = append(programBlocks, bumperDef)
			}
		}
	} else {
		for r := 0; r < maxRounds; r++ {
			// 1. Pasporto
			if r < len(pasporto) {
				epNum := r + 1
				t := fmt.Sprintf("Pasporto al la Tuta Mondo - Epizodo %d", epNum)
				d := fmt.Sprintf("Epizodo %d de la internacia komedia realspektaklo Pasporto al la Tuta Mondo.", epNum)
				if info, ok := pasportoEpInfo[epNum]; ok {
					t = fmt.Sprintf("Pasporto al la Tuta Mondo - Epizodo %d: %s", epNum, info[0])
					if info[1] != "" {
						d = info[1]
					}
				}
				programBlocks = append(programBlocks, blockDef{
					title:    t,
					desc:     d,
					category: "Kurso / Komedio",
					segs:     pasporto[r],
				})
				if len(bumperSegs) > 0 {
					programBlocks = append(programBlocks, bumperDef)
				}
			}

			// 2. Senlime
			if r < len(senlime) {
				epNum := r + 1
				t := fmt.Sprintf("Esperanto Senlime - Epizodo %d", epNum)
				d := fmt.Sprintf("Moderna vojaĝa kaj lingva realspektaklo tra Eŭropo (Epizodo %d).", epNum)
				if info, ok := senlimeEpInfo[epNum]; ok {
					t = fmt.Sprintf("Esperanto Senlime - Epizodo %d: %s", epNum, info[0])
					if info[1] != "" {
						d = info[1]
					}
				}
				programBlocks = append(programBlocks, blockDef{
					title:    t,
					desc:     d,
					category: "Realspektaklo / Junularo",
					segs:     senlime[r],
				})
				if len(bumperSegs) > 0 {
					programBlocks = append(programBlocks, bumperDef)
				}
			}

			// 3. Music Video Block
			if r < len(mvBlocks) {
				programBlocks = append(programBlocks, blockDef{
					title:    "Esperanto-Muziko (Muzikvideoj)",
					desc:     "Kolekto de popularaj Esperanto-muzikvideoj kaj kantoj.",
					category: "Muziko",
					segs:     mvBlocks[r],
				})
				if len(bumperSegs) > 0 {
					programBlocks = append(programBlocks, bumperDef)
				}
			}

			// 4. Special (Docs / Mazi)
			if r < len(specials) {
				sSegs := specials[r]
				sTitle := "Dokumenta Filmo"
				sDesc := "Esperanto-dokumentario aŭ animacia klasikaĵo."
				sCat := "Dokumentario"
				if len(sSegs) > 0 {
					first := sSegs[0]
					if strings.Contains(first, "mazi") {
						sTitle = "Mazi en Gondolando"
						sDesc = "Klasika animacia Esperanto-kurso kun Mazi, Silvia kaj Bob."
						sCat = "Animacio"
					} else if strings.Contains(first, "kef2005") {
						sTitle = "KEF 2005: La Plejpleja Festivalo"
						sDesc = "Kultura Esperanto-Festivalo en Helsinki."
					} else if strings.Contains(first, "dok_estas") {
						sTitle = "Esperanto Estas: Dokumentario"
						sDesc = "Dokumenta serio pri la historio kaj moderna komunumo de Esperanto."
					}
				}
				programBlocks = append(programBlocks, blockDef{
					title:    sTitle,
					desc:     sDesc,
					category: sCat,
					segs:     sSegs,
				})
				if len(bumperSegs) > 0 {
					programBlocks = append(programBlocks, bumperDef)
				}
			}
		}
	}

	var scheduled []ScheduledSegment
	for _, pb := range programBlocks {
		for i, seg := range pb.segs {
			scheduled = append(scheduled, ScheduledSegment{
				Name:         seg,
				Title:        pb.title,
				Desc:         pb.desc,
				Category:     pb.category,
				IsTransition: i == 0,
			})
		}
	}

	ls.schedule = scheduled
}

func (ls *LinearStation) buildDefaultSchedule(allSegs []string) {
	var scheduled []ScheduledSegment
	var lastPrefix string

	for _, seg := range allSegs {
		idx := strings.LastIndex(seg, "_")
		prefix := seg
		if idx != -1 {
			prefix = seg[:idx]
		}
		isTrans := prefix != lastPrefix
		lastPrefix = prefix

		cleanName := strings.Title(strings.ReplaceAll(prefix, "_", " "))
		scheduled = append(scheduled, ScheduledSegment{
			Name:         seg,
			Title:        cleanName,
			Desc:         fmt.Sprintf("Broadcast of %s", cleanName),
			Category:     "General",
			IsTransition: isTrans,
		})
	}
	ls.schedule = scheduled
}

func (ls *LinearStation) Playlist(standbyTS string) string {
	ls.mu.RLock()
	if time.Since(ls.lastScan) > 10*time.Minute || len(ls.schedule) == 0 {
		ls.mu.RUnlock()
		ls.rebuildSchedule()
		ls.mu.RLock()
	}
	defer ls.mu.RUnlock()

	targetDur := int(ls.segDuration) + 1
	if len(ls.schedule) == 0 {
		return fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:%.6f,\n%s\n#EXT-X-ENDLIST\n",
			targetDur, ls.segDuration, standbyTS)
	}

	totalSegs := len(ls.schedule)
	totalCycleTime := float64(totalSegs) * ls.segDuration

	now := float64(time.Now().UnixNano()) / 1e9
	totalLoops := int(now / totalCycleTime)
	currentOffset := float64(int64(now*1000)%(int64(totalCycleTime*1000))) / 1000.0
	currentIdx := int(currentOffset / ls.segDuration)
	mediaSequence := int(now / ls.segDuration)

	// Calculate discontinuity sequence
	totalDiscsPerCycle := len(ls.discOffsets)
	discsBefore := 0
	for _, dIdx := range ls.discOffsets {
		if dIdx < currentIdx {
			discsBefore++
		} else {
			break
		}
	}
	discSeq := totalLoops*totalDiscsPerCycle + discsBefore

	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	sb.WriteString("#EXT-X-VERSION:3\n")
	fmt.Fprintf(&sb, "#EXT-X-TARGETDURATION:%d\n", targetDur)
	fmt.Fprintf(&sb, "#EXT-X-MEDIA-SEQUENCE:%d\n", mediaSequence)
	fmt.Fprintf(&sb, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", discSeq)

	for k := 0; k < 5; k++ {
		segIdx := (currentIdx + k) % totalSegs
		item := ls.schedule[segIdx]

		// Output #EXT-X-DISCONTINUITY if this segment starts a new stream/program
		if item.IsTransition {
			sb.WriteString("#EXT-X-DISCONTINUITY\n")
		}

		fmt.Fprintf(&sb, "#EXTINF:%.6f,\n", ls.segDuration)
		fmt.Fprintf(&sb, "/iptv/%s/%s\n", ls.prefix, item.Name)
	}

	return sb.String()
}
