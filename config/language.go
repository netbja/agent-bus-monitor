package config

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Language is one ISO 639-1 language: its code, its English name and its
// own name (the autonym, lower case where its language writes it so).
type Language struct {
	Code    string
	English string
	Native  string
}

// languages is every ISO 639-1 code, in code order, with its names: what
// person.language takes (the person's answer to the language-setting spec
// §7 Q2: any valid code) and what busmon's list shows (language-picker).
// The English names are ISO 639's. The native names come from sources, not
// memory (reviewer-codex, master): CLDR's own name for the language
// (golang.org/x/text v0.38.0 display.Self, read once by a throwaway program,
// no dependency) wherever CLDR has one; else, and for no, tl and tw (which
// CLDR maps to Bokmål, Filipino and Akan), the first endonym of Wikipedia's
// "List of ISO 639 language codes" (2026-11-21) without its transliteration;
// two exceptions: ab is the language's name, аҧсуа бызшәа (the list gives
// the people's word), and jv the list's Latin form, basa Jawa (most
// terminal fonts lack the Javanese script).
var languages = []Language{
	{"aa", "Afar", "Qafar"}, {"ab", "Abkhazian", "аҧсуа бызшәа"}, {"ae", "Avestan", "Upastawakaēna"},
	{"af", "Afrikaans", "Afrikaans"}, {"ak", "Akan", "Akan"}, {"am", "Amharic", "አማርኛ"},
	{"an", "Aragonese", "aragonés"}, {"ar", "Arabic", "العربية"}, {"as", "Assamese", "অসমীয়া"},
	{"av", "Avaric", "авар мацӏ"}, {"ay", "Aymara", "Aymara"}, {"az", "Azerbaijani", "azərbaycan"},
	{"ba", "Bashkir", "башҡорт теле"}, {"be", "Belarusian", "беларуская"}, {"bg", "Bulgarian", "български"},
	{"bi", "Bislama", "Bislama"}, {"bm", "Bambara", "bamanakan"}, {"bn", "Bengali", "বাংলা"},
	{"bo", "Tibetan", "བོད་སྐད་"}, {"br", "Breton", "brezhoneg"}, {"bs", "Bosnian", "bosanski"},
	{"ca", "Catalan", "català"}, {"ce", "Chechen", "нохчийн"}, {"ch", "Chamorro", "Finu' Chamoru"},
	{"co", "Corsican", "corsu"}, {"cr", "Cree", "ᓀᐦᐃᔭᐁᐧᐃᐧᐣ"}, {"cs", "Czech", "čeština"},
	{"cu", "Church Slavonic", "славе́нскїй ѧ҆зы́къ"}, {"cv", "Chuvash", "чӑвашла"}, {"cy", "Welsh", "Cymraeg"},
	{"da", "Danish", "dansk"}, {"de", "German", "Deutsch"}, {"dv", "Divehi", "ދިވެހި"},
	{"dz", "Dzongkha", "རྫོང་ཁ"}, {"ee", "Ewe", "Eʋegbe"}, {"el", "Greek", "Ελληνικά"},
	{"en", "English", "English"}, {"eo", "Esperanto", "esperanto"}, {"es", "Spanish", "español"},
	{"et", "Estonian", "eesti"}, {"eu", "Basque", "euskara"}, {"fa", "Persian", "فارسی"},
	{"ff", "Fulah", "Pulaar"}, {"fi", "Finnish", "suomi"}, {"fj", "Fijian", "Na Vosa Vakaviti"},
	{"fo", "Faroese", "føroyskt"}, {"fr", "French", "français"}, {"fy", "Western Frisian", "Frysk"},
	{"ga", "Irish", "Gaeilge"}, {"gd", "Scottish Gaelic", "Gàidhlig"}, {"gl", "Galician", "galego"},
	{"gn", "Guarani", "Avañe'ẽ"}, {"gu", "Gujarati", "ગુજરાતી"}, {"gv", "Manx", "Gaelg"},
	{"ha", "Hausa", "Hausa"}, {"he", "Hebrew", "עברית"}, {"hi", "Hindi", "हिन्दी"},
	{"ho", "Hiri Motu", "Hiri Motu"}, {"hr", "Croatian", "hrvatski"}, {"ht", "Haitian Creole", "Kreyòl ayisyen"},
	{"hu", "Hungarian", "magyar"}, {"hy", "Armenian", "հայերեն"}, {"hz", "Herero", "Otjiherero"},
	{"ia", "Interlingua", "Interlingua"}, {"id", "Indonesian", "Indonesia"}, {"ie", "Interlingue", "Interlingue"},
	{"ig", "Igbo", "Igbo"}, {"ii", "Sichuan Yi", "ꆈꌠꉙ"}, {"ik", "Inupiaq", "Iñupiaq"},
	{"io", "Ido", "Ido"}, {"is", "Icelandic", "íslenska"}, {"it", "Italian", "italiano"},
	{"iu", "Inuktitut", "ᐃᓄᒃᑎᑐᑦ"}, {"ja", "Japanese", "日本語"}, {"jv", "Javanese", "basa Jawa"},
	{"ka", "Georgian", "ქართული"}, {"kg", "Kongo", "Kikongo"}, {"ki", "Kikuyu", "Gikuyu"},
	{"kj", "Kuanyama", "Oshikwanyama"}, {"kk", "Kazakh", "қазақ тілі"}, {"kl", "Kalaallisut", "kalaallisut"},
	{"km", "Khmer", "ខ្មែរ"}, {"kn", "Kannada", "ಕನ್ನಡ"}, {"ko", "Korean", "한국어"},
	{"kr", "Kanuri", "كَنُرِيِه"}, {"ks", "Kashmiri", "کٲشُر"}, {"ku", "Kurdish", "کوردی"},
	{"kv", "Komi", "коми кыв"}, {"kw", "Cornish", "kernewek"}, {"ky", "Kyrgyz", "кыргызча"},
	{"la", "Latin", "Latinum"}, {"lb", "Luxembourgish", "Lëtzebuergesch"}, {"lg", "Ganda", "Luganda"},
	{"li", "Limburgish", "Limburgisch"}, {"ln", "Lingala", "lingála"}, {"lo", "Lao", "ລາວ"},
	{"lt", "Lithuanian", "lietuvių"}, {"lu", "Luba-Katanga", "Tshiluba"}, {"lv", "Latvian", "latviešu"},
	{"mg", "Malagasy", "Malagasy"}, {"mh", "Marshallese", "kajin M̧ajeļ"}, {"mi", "Maori", "reo Māori"},
	{"mk", "Macedonian", "македонски"}, {"ml", "Malayalam", "മലയാളം"}, {"mn", "Mongolian", "монгол"},
	{"mr", "Marathi", "मराठी"}, {"ms", "Malay", "Melayu"}, {"mt", "Maltese", "Malti"},
	{"my", "Burmese", "မြန်မာ"}, {"na", "Nauru", "dorerin Naoe"}, {"nb", "Norwegian Bokmål", "norsk bokmål"},
	{"nd", "North Ndebele", "isiNdebele"}, {"ne", "Nepali", "नेपाली"}, {"ng", "Ndonga", "Ndonga"},
	{"nl", "Dutch", "Nederlands"}, {"nn", "Norwegian Nynorsk", "nynorsk"}, {"no", "Norwegian", "norsk"},
	{"nr", "South Ndebele", "isiNdebele"}, {"nv", "Navajo", "Diné bizaad"}, {"ny", "Chichewa", "Chichewa"},
	{"oc", "Occitan", "occitan"}, {"oj", "Ojibwe", "ᐊᓂᔑᓈᐯᒧᐎᓐ"}, {"om", "Oromo", "Oromoo"},
	{"or", "Odia", "ଓଡ଼ିଆ"}, {"os", "Ossetian", "ирон"}, {"pa", "Punjabi", "ਪੰਜਾਬੀ"},
	{"pi", "Pali", "Pāli"}, {"pl", "Polish", "polski"}, {"ps", "Pashto", "پښتو"},
	{"pt", "Portuguese", "português"}, {"qu", "Quechua", "Runasimi"}, {"rm", "Romansh", "rumantsch"},
	{"rn", "Kirundi", "Ikirundi"}, {"ro", "Romanian", "română"}, {"ru", "Russian", "русский"},
	{"rw", "Kinyarwanda", "Kinyarwanda"}, {"sa", "Sanskrit", "संस्कृतम्"}, {"sc", "Sardinian", "sardu"},
	{"sd", "Sindhi", "سنڌي"}, {"se", "Northern Sami", "davvisámegiella"}, {"sg", "Sango", "Sängö"},
	{"si", "Sinhala", "සිංහල"}, {"sk", "Slovak", "slovenčina"}, {"sl", "Slovenian", "slovenščina"},
	{"sm", "Samoan", "gagana Sāmoa"}, {"sn", "Shona", "chiShona"}, {"so", "Somali", "Soomaali"},
	{"sq", "Albanian", "shqip"}, {"sr", "Serbian", "српски"}, {"ss", "Swati", "siSwati"},
	{"st", "Southern Sotho", "Sesotho"}, {"su", "Sundanese", "basa Sunda"}, {"sv", "Swedish", "svenska"},
	{"sw", "Swahili", "Kiswahili"}, {"ta", "Tamil", "தமிழ்"}, {"te", "Telugu", "తెలుగు"},
	{"tg", "Tajik", "тоҷикӣ"}, {"th", "Thai", "ไทย"}, {"ti", "Tigrinya", "ትግርኛ"},
	{"tk", "Turkmen", "Türkmen dili"}, {"tl", "Tagalog", "Wikang Tagalog"}, {"tn", "Tswana", "Setswana"},
	{"to", "Tonga", "lea fakatonga"}, {"tr", "Turkish", "Türkçe"}, {"ts", "Tsonga", "Xitsonga"},
	{"tt", "Tatar", "татар"}, {"tw", "Twi", "Twi"}, {"ty", "Tahitian", "reo Tahiti"},
	{"ug", "Uyghur", "ئۇيغۇرچە"}, {"uk", "Ukrainian", "українська"}, {"ur", "Urdu", "اردو"},
	{"uz", "Uzbek", "o‘zbek"}, {"ve", "Venda", "Tshivenḓa"}, {"vi", "Vietnamese", "Tiếng Việt"},
	{"vo", "Volapük", "Volapük"}, {"wa", "Walloon", "walon"}, {"wo", "Wolof", "Wolof"},
	{"xh", "Xhosa", "isiXhosa"}, {"yi", "Yiddish", "ייִדיש"}, {"yo", "Yoruba", "Èdè Yorùbá"},
	{"za", "Zhuang", "話僮"}, {"zh", "Chinese", "中文"}, {"zu", "Zulu", "isiZulu"},
}

// iso6391 indexes languages by code.
var iso6391 = func() map[string]Language {
	m := make(map[string]Language, len(languages))
	for _, l := range languages {
		m[l.Code] = l
	}
	return m
}()

// Languages is the ISO 639-1 table, in code order (a copy).
func Languages() []Language { return append([]Language(nil), languages...) }

// LanguageOf is code's language, false when code is not an ISO 639-1 code.
func LanguageOf(code string) (Language, bool) {
	l, ok := iso6391[code]
	return l, ok
}

// isLanguage: s is an ISO 639-1 code, lower case.
func isLanguage(s string) bool {
	_, ok := iso6391[s]
	return ok
}

// languageOptions are person.language's choices: each code, labelled with
// its own name (a capital first, as a list item starts, but for a
// lower-case prefix) and its English
// name in brackets when they differ: "Français (French)", "English".
func languageOptions() []Option {
	out := make([]Option, len(languages))
	for i, l := range languages {
		native := l.Native
		// A capital first, as a list item starts, unless the name has one
		// further on: isiZulu and chiShona keep their lower-case prefix
		// (foureyes).
		if r, n := utf8.DecodeRuneInString(native); unicode.IsLower(r) && !strings.ContainsFunc(native[n:], unicode.IsUpper) {
			native = string(unicode.ToUpper(r)) + native[n:]
		}
		label := native
		if !strings.EqualFold(native, l.English) {
			label += " (" + l.English + ")"
		}
		out[i] = Option{Value: l.Code, Label: label}
	}
	return out
}
