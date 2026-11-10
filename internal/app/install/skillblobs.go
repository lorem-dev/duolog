package install

// writtenBlobs are the content ids (contentID) of every text of a bundled
// skill file that a duolog wrote, for both agents, so a file with one of
// these ids is duolog's and unchanged, and install may replace it.
//
// The first part holds Git's object ids of every version committed before
// duolog kept its skills at user scope and marked them with its version: an
// install up to then wrote one of these into the project, or at user scope.
// It came from
//
//	git log <commit> --format= --raw --no-abbrev -- plugin/claude/skills plugin/codex/skills
//
// with the deleted entries left out. A file with no version marker keeps its
// Git object id as its content id.
//
// The second part holds the ids of the marked skills, with their version
// replaced, so a release adds none. A change to a skill's text adds its new
// id here; TestEveryBundledSkillFileIsAKnownBlob names it. Never remove one:
// a later duolog must know every text an earlier one wrote.
var writtenBlobs = map[string]bool{
	"00a28226bf58d73d2c5820975f1bbdc9e9604b27": true, // claude/skills/duolog-peers/SKILL.md
	"70fa230d0228fbc6570a24c1603ecfd54e9b9661": true, // codex/skills/duolog-peers/SKILL.md
	"08f4ea26d84e7d8978da3dd0fe4725f8717facb2": true, // codex/skills/duolog-calibrate/SKILL.md
	"0ca031574857b7f17bbf12f15c4f6841bb6b1154": true, // claude/skills/duolog-peers/SKILL.md
	"13bdeefb71fdcde053ca45ec6aa29883122b57e6": true, // claude/skills/duolog-calibrate/SKILL.md
	"147b92ab3f52010cb164385731264e16a195f7cd": true, // codex/skills/duolog/SKILL.md
	"18535fef540b7c47d3a55dfd077765d3cdba58bc": true, // claude/skills/duolog/SKILL.md
	"257995d344a53c6c3de78b053f7cfccabca42c21": true, // codex/skills/duolog/SKILL.md
	"268c74276ea6ed53a44ca4d4d584e4f94bb1980f": true, // codex/skills/duolog/SKILL.md
	"2e1608744688fe667a3ee45e32f93cea7408f2c3": true, // claude/skills/duolog/SKILL.md
	"3b6d3beba0e658d0be03d1da5d99ec7d67622f15": true, // claude/skills/duolog/SKILL.md
	"3e81c2b2553f5eefc0162789ed9d4eb2b7831115": true, // codex/skills/duolog-peers/SKILL.md
	"4c8b4d2869707f0f34e8a4337b48ecaccb26e737": true, // codex/skills/duolog/SKILL.md
	"5009b80a687f32b0592a1e3b7a3316fe06c15fa9": true, // codex/skills/duolog/SKILL.md
	"51dc2ad61f2c586d769cb51b6f9d7429cba7e909": true, // claude/skills/duolog/SKILL.md
	"52a6e441e16076faf85e42d33e1f19f6d2504328": true, // codex/skills/duolog/SKILL.md
	"57226df2082b4ad702d800ecb65cb1a27c708cd3": true, // claude/skills/duolog/SKILL.md
	"57f0234712c6680fb75da91b247ea12f2e1031b1": true, // claude/skills/duolog/SKILL.md
	"5ab01be047892312a2264d5861ef54879566ae94": true, // codex/skills/duolog/SKILL.md
	"5b1f887a9138416870f64ba2fe21602e4a59cfa7": true, // codex/skills/duolog-calibrate/agents/openai.yaml
	"6cd67159af6928389673281fa54a8d14e755db38": true, // claude/skills/duolog/SKILL.md
	"6e9b919eb0bb475bd80e29cf4565e791cfe5a766": true, // codex/skills/duolog/SKILL.md
	"77b56ac47f5a542b71c061b1207e40a93726f347": true, // claude/skills/duolog/SKILL.md
	"7a08fb9a84186dc37ee2b199ecd3898685eb7841": true, // claude/skills/duolog/SKILL.md
	"7e800e24e5c86e5ba13af7e08c186e8c827ad5ee": true, // claude/skills/duolog/SKILL.md
	"862ee44aab6c08b00e997ff3f86ad17d894df669": true, // claude/skills/duolog/SKILL.md
	"886572a906bda0ebae0916b3fc1d917c2f71fd6d": true, // codex/skills/duolog/SKILL.md
	"8ef70622757e99110300bb71f79f06fc809588b5": true, // codex/skills/duolog/SKILL.md
	"94d7dff10cdcd35e988631389fe38c37627ab88b": true, // codex/skills/duolog/SKILL.md
	"9aa7e9b56c4754bb4a0900bbbbf24940ee5eac48": true, // codex/skills/duolog/SKILL.md
	"a7e19db12b709ccd08ccff46d49ef6e26bad4790": true, // claude/skills/duolog-peers/SKILL.md
	"b64a0bd3a383e5053d02db640c15d1510604df69": true, // claude/skills/duolog/SKILL.md
	"b67c965d66c123ed34d2d26c84ffc8075bfc1a00": true, // claude and codex skills/duolog-skill-check/SKILL.md
	"b709352c65685e30be04ecccf5d64f67ac7dd3ee": true, // claude/skills/duolog/SKILL.md
	"c1a02cd4eebca0024dee3edcf1f4b05b580d85bb": true, // codex/skills/duolog/SKILL.md
	"c73a8600786f257f26a1a009749683b9f2d52bc2": true, // codex/skills/duolog/SKILL.md
	"c7cd869cafbfe13dee3cd09882ccc9657b11de03": true, // claude/skills/duolog/SKILL.md
	"cc4cc930ebabd547b4db45cb4abf0d170dbd5345": true, // codex/skills/duolog/SKILL.md
	"d02c575f9aac556976312195ac8e974b7f48c677": true, // codex/skills/duolog/SKILL.md
	"d523219b2bee298071037fe1965d2d2d3532dc38": true, // codex/skills/duolog/SKILL.md
	"df083249e7e7814a2376e24584f4678ac4901719": true, // claude/skills/duolog/SKILL.md
	"e4d9d587b971d07f2480b48580eb845e10a02a8b": true, // claude/skills/duolog/SKILL.md
	"e8a61ff70e65b0b18644a1299b961409146a4e45": true, // claude/skills/duolog/SKILL.md
	"ead1992773d57fa500a30de99cbcae234a271401": true, // codex/skills/duolog/SKILL.md
	"ebe301c27b0b57b4e6d2beaf204d54c7bb9a582b": true, // claude/skills/duolog/SKILL.md
	"efec076d2806b3bf72f09142072dde85d25b26a5": true, // codex/skills/duolog/SKILL.md
	"f33b49b3f7a3408eaaca40c01874dab51a459a42": true, // claude/skills/duolog/SKILL.md

	// Marked with the duolog version, from 1.6.0 on.
	"acc5ad46cb496248dcf4de1b9e27ad4c32d77cd3": true, // claude/skills/duolog/SKILL.md
	"b7b52066313468dd992cd44e025f7295ec31e46d": true, // codex/skills/duolog/SKILL.md
	"02f39f2cef363769fb599082961a256a974a0dbc": true, // codex/skills/duolog-calibrate/SKILL.md
	"1799c0136c82ca3d882c3283bd9e2fb0d8c22732": true, // codex/skills/duolog/SKILL.md
	"948c568aeaf7320b7a9c6bd00eb35b1c7e98fd01": true, // claude/skills/duolog-calibrate/SKILL.md
	"950e80d7cd796377c4afd078dd59dd140af4c3de": true, // claude/skills/duolog/SKILL.md
	"9e56637ad46836198a9e1c9e3dea0dc8c7a1c064": true, // claude and codex skills/duolog-skill-check/SKILL.md
	"bacbf5a509a0ff8dd63c5f7037061d0c5016d4a0": true, // codex/skills/duolog-peers/SKILL.md
	"e6e7a9173aa3570aacec9bad8cd44b875bbd93ad": true, // claude/skills/duolog-peers/SKILL.md
	"ecfccca6cc9cde027bc063369786cfc698cf2253": true, // codex/skills/duolog/SKILL.md
	"fe55bc57a2eaa0f5ab5961f6366fe6b9b9dcb11e": true, // claude/skills/duolog/SKILL.md
	"f5746d8674265be02c69034e42515428e7d6d4c2": true, // claude/skills/duolog/SKILL.md
	"ba2c8c7b4d397ee6f2c3e082611f722fb301bd2c": true, // codex/skills/duolog/SKILL.md
	"1cb69ff5b3b7b73e15a2cc0ebff87ce17ad1ed49": true, // claude/skills/duolog-peers/SKILL.md
	"b4a557a87b43b0fa98ee10272514de732de69708": true, // codex/skills/duolog-peers/SKILL.md
	"3cd70daf2ccb6e866563e8da18418a8c73372d79": true, // claude/skills/duolog-peers/SKILL.md
	"7b9638c48bd7d0319b1281be722006a09bd0494c": true, // codex/skills/duolog-peers/SKILL.md
	"8ca5df6db4eee6f958279aa162e308efd0551d61": true, // claude/skills/duolog/SKILL.md
	"b7e054f1adc3da2236203508b9bb23ec4fe76046": true, // codex/skills/duolog/SKILL.md
	"e288a41202255cddb8d60da4e1603108c670cbc9": true, // claude/skills/duolog/SKILL.md
	"67378db6f3480f6c7c7b54795ea50402d82fbb66": true, // codex/skills/duolog/SKILL.md
	"4fe372802ad0e35e6d40f0bd3d74149d6e682069": true, // claude/skills/duolog/SKILL.md
	"2d58959aa676cc271153e5d6c9509cb34612bcde": true, // codex/skills/duolog/SKILL.md
	"93187f06340a7315da0bbea39843c8b8a4b1b196": true, // claude/skills/duolog-update/SKILL.md
	"65c864a0270ba058bae537a740c41ddd77b55975": true, // codex/skills/duolog-update/SKILL.md
}
