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
var languages = []Language{
	{"aa", "Afar", "Afaraf"}, {"ab", "Abkhazian", "аҧсуа бызшәа"}, {"ae", "Avestan", "avesta"},
	{"af", "Afrikaans", "Afrikaans"}, {"ak", "Akan", "Akan"}, {"am", "Amharic", "አማርኛ"},
	{"an", "Aragonese", "aragonés"}, {"ar", "Arabic", "العربية"}, {"as", "Assamese", "অসমীয়া"},
	{"av", "Avaric", "авар мацӀ"}, {"ay", "Aymara", "aymar aru"}, {"az", "Azerbaijani", "azərbaycan dili"},
	{"ba", "Bashkir", "башҡорт теле"}, {"be", "Belarusian", "беларуская мова"}, {"bg", "Bulgarian", "български език"},
	{"bi", "Bislama", "Bislama"}, {"bm", "Bambara", "bamanankan"}, {"bn", "Bengali", "বাংলা"},
	{"bo", "Tibetan", "བོད་ཡིག"}, {"br", "Breton", "brezhoneg"}, {"bs", "Bosnian", "bosanski jezik"},
	{"ca", "Catalan", "català"}, {"ce", "Chechen", "нохчийн мотт"}, {"ch", "Chamorro", "Chamoru"},
	{"co", "Corsican", "corsu"}, {"cr", "Cree", "ᓀᐦᐃᔭᐍᐏᐣ"}, {"cs", "Czech", "čeština"},
	{"cu", "Church Slavonic", "ѩзыкъ словѣньскъ"}, {"cv", "Chuvash", "чӑваш чӗлхи"}, {"cy", "Welsh", "Cymraeg"},
	{"da", "Danish", "dansk"}, {"de", "German", "Deutsch"}, {"dv", "Divehi", "ދިވެހި"},
	{"dz", "Dzongkha", "རྫོང་ཁ"}, {"ee", "Ewe", "Eʋegbe"}, {"el", "Greek", "ελληνικά"},
	{"en", "English", "English"}, {"eo", "Esperanto", "Esperanto"}, {"es", "Spanish", "español"},
	{"et", "Estonian", "eesti"}, {"eu", "Basque", "euskara"}, {"fa", "Persian", "فارسی"},
	{"ff", "Fulah", "Fulfulde"}, {"fi", "Finnish", "suomi"}, {"fj", "Fijian", "vosa Vakaviti"},
	{"fo", "Faroese", "føroyskt"}, {"fr", "French", "français"}, {"fy", "Western Frisian", "Frysk"},
	{"ga", "Irish", "Gaeilge"}, {"gd", "Scottish Gaelic", "Gàidhlig"}, {"gl", "Galician", "galego"},
	{"gn", "Guarani", "Avañe'ẽ"}, {"gu", "Gujarati", "ગુજરાતી"}, {"gv", "Manx", "Gaelg"},
	{"ha", "Hausa", "Hausa"}, {"he", "Hebrew", "עברית"}, {"hi", "Hindi", "हिन्दी"},
	{"ho", "Hiri Motu", "Hiri Motu"}, {"hr", "Croatian", "hrvatski"}, {"ht", "Haitian Creole", "Kreyòl ayisyen"},
	{"hu", "Hungarian", "magyar"}, {"hy", "Armenian", "Հայերեն"}, {"hz", "Herero", "Otjiherero"},
	{"ia", "Interlingua", "Interlingua"}, {"id", "Indonesian", "Bahasa Indonesia"}, {"ie", "Interlingue", "Interlingue"},
	{"ig", "Igbo", "Asụsụ Igbo"}, {"ii", "Sichuan Yi", "ꆈꌠꉙ"}, {"ik", "Inupiaq", "Iñupiaq"},
	{"io", "Ido", "Ido"}, {"is", "Icelandic", "íslenska"}, {"it", "Italian", "italiano"},
	{"iu", "Inuktitut", "ᐃᓄᒃᑎᑐᑦ"}, {"ja", "Japanese", "日本語"}, {"jv", "Javanese", "basa Jawa"},
	{"ka", "Georgian", "ქართული"}, {"kg", "Kongo", "Kikongo"}, {"ki", "Kikuyu", "Gĩkũyũ"},
	{"kj", "Kuanyama", "Kuanyama"}, {"kk", "Kazakh", "қазақ тілі"}, {"kl", "Kalaallisut", "kalaallisut"},
	{"km", "Khmer", "ខ្មែរ"}, {"kn", "Kannada", "ಕನ್ನಡ"}, {"ko", "Korean", "한국어"},
	{"kr", "Kanuri", "Kanuri"}, {"ks", "Kashmiri", "कॉशुर"}, {"ku", "Kurdish", "Kurdî"},
	{"kv", "Komi", "коми кыв"}, {"kw", "Cornish", "Kernewek"}, {"ky", "Kyrgyz", "кыргызча"},
	{"la", "Latin", "latine"}, {"lb", "Luxembourgish", "Lëtzebuergesch"}, {"lg", "Ganda", "Luganda"},
	{"li", "Limburgish", "Limburgs"}, {"ln", "Lingala", "lingála"}, {"lo", "Lao", "ພາສາລາວ"},
	{"lt", "Lithuanian", "lietuvių kalba"}, {"lu", "Luba-Katanga", "Kiluba"}, {"lv", "Latvian", "latviešu valoda"},
	{"mg", "Malagasy", "fiteny malagasy"}, {"mh", "Marshallese", "Kajin M̧ajeļ"}, {"mi", "Maori", "te reo Māori"},
	{"mk", "Macedonian", "македонски јазик"}, {"ml", "Malayalam", "മലയാളം"}, {"mn", "Mongolian", "монгол хэл"},
	{"mr", "Marathi", "मराठी"}, {"ms", "Malay", "Bahasa Melayu"}, {"mt", "Maltese", "Malti"},
	{"my", "Burmese", "ဗမာစာ"}, {"na", "Nauru", "Dorerin Naoero"}, {"nb", "Norwegian Bokmål", "norsk bokmål"},
	{"nd", "North Ndebele", "isiNdebele"}, {"ne", "Nepali", "नेपाली"}, {"ng", "Ndonga", "Owambo"},
	{"nl", "Dutch", "Nederlands"}, {"nn", "Norwegian Nynorsk", "norsk nynorsk"}, {"no", "Norwegian", "norsk"},
	{"nr", "South Ndebele", "isiNdebele"}, {"nv", "Navajo", "Diné bizaad"}, {"ny", "Chichewa", "chiCheŵa"},
	{"oc", "Occitan", "occitan"}, {"oj", "Ojibwe", "ᐊᓂᔑᓈᐯᒧᐎᓐ"}, {"om", "Oromo", "Afaan Oromoo"},
	{"or", "Odia", "ଓଡ଼ିଆ"}, {"os", "Ossetian", "ирон æвзаг"}, {"pa", "Punjabi", "ਪੰਜਾਬੀ"},
	{"pi", "Pali", "पाऴि"}, {"pl", "Polish", "polski"}, {"ps", "Pashto", "پښتو"},
	{"pt", "Portuguese", "português"}, {"qu", "Quechua", "Runa Simi"}, {"rm", "Romansh", "rumantsch grischun"},
	{"rn", "Kirundi", "Ikirundi"}, {"ro", "Romanian", "română"}, {"ru", "Russian", "русский"},
	{"rw", "Kinyarwanda", "Ikinyarwanda"}, {"sa", "Sanskrit", "संस्कृतम्"}, {"sc", "Sardinian", "sardu"},
	{"sd", "Sindhi", "سنڌي"}, {"se", "Northern Sami", "davvisámegiella"}, {"sg", "Sango", "yângâ tî sängö"},
	{"si", "Sinhala", "සිංහල"}, {"sk", "Slovak", "slovenčina"}, {"sl", "Slovenian", "slovenščina"},
	{"sm", "Samoan", "gagana fa'a Samoa"}, {"sn", "Shona", "chiShona"}, {"so", "Somali", "Soomaaliga"},
	{"sq", "Albanian", "shqip"}, {"sr", "Serbian", "српски језик"}, {"ss", "Swati", "SiSwati"},
	{"st", "Southern Sotho", "Sesotho"}, {"su", "Sundanese", "Basa Sunda"}, {"sv", "Swedish", "svenska"},
	{"sw", "Swahili", "Kiswahili"}, {"ta", "Tamil", "தமிழ்"}, {"te", "Telugu", "తెలుగు"},
	{"tg", "Tajik", "тоҷикӣ"}, {"th", "Thai", "ไทย"}, {"ti", "Tigrinya", "ትግርኛ"},
	{"tk", "Turkmen", "Türkmençe"}, {"tl", "Tagalog", "Wikang Tagalog"}, {"tn", "Tswana", "Setswana"},
	{"to", "Tonga", "faka Tonga"}, {"tr", "Turkish", "Türkçe"}, {"ts", "Tsonga", "Xitsonga"},
	{"tt", "Tatar", "татар теле"}, {"tw", "Twi", "Twi"}, {"ty", "Tahitian", "Reo Tahiti"},
	{"ug", "Uyghur", "ئۇيغۇرچە"}, {"uk", "Ukrainian", "українська"}, {"ur", "Urdu", "اردو"},
	{"uz", "Uzbek", "oʻzbek"}, {"ve", "Venda", "Tshivenḓa"}, {"vi", "Vietnamese", "Tiếng Việt"},
	{"vo", "Volapük", "Volapük"}, {"wa", "Walloon", "walon"}, {"wo", "Wolof", "Wollof"},
	{"xh", "Xhosa", "isiXhosa"}, {"yi", "Yiddish", "ייִדיש"}, {"yo", "Yoruba", "Yorùbá"},
	{"za", "Zhuang", "Saɯ cueŋƅ"}, {"zh", "Chinese", "中文"}, {"zu", "Zulu", "isiZulu"},
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
// its own name (a capital first, as a list item starts) and its English
// name in brackets when they differ: "Français (French)", "English".
func languageOptions() []Option {
	out := make([]Option, len(languages))
	for i, l := range languages {
		native := l.Native
		if r, n := utf8.DecodeRuneInString(native); unicode.IsLower(r) {
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
