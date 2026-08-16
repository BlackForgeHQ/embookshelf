# Changelog

## [0.6.8](https://github.com/BlackForgeHQ/embookshelf/compare/v0.6.7...v0.6.8) (2026-08-16)


### Features

* **ui:** lib/artifactRun — one artifact-status vocabulary and its poll predicate; the guide panel stops hanging on Converting ([#350](https://github.com/BlackForgeHQ/embookshelf/issues/350)) ([4e42d9d](https://github.com/BlackForgeHQ/embookshelf/commit/4e42d9df3713f64c4fad1f26b3877b8ae82abeb9))


### Bug Fixes

* **e2e:** stats subtitle copy caught up with the design-audit sweep; notebook waiters arm before navigation ([ccdeb11](https://github.com/BlackForgeHQ/embookshelf/commit/ccdeb1108d73233ad257c5bdd5d26fca7f642f72))
* **ui:** guide/narration SSE events bust the keys the panels read — one declared event→keys table ([#349](https://github.com/BlackForgeHQ/embookshelf/issues/349)) ([21325ff](https://github.com/BlackForgeHQ/embookshelf/commit/21325ff004475f54d32c279cf46c95896be4511a))

## [0.6.7](https://github.com/BlackForgeHQ/embookshelf/compare/v0.6.6...v0.6.7) (2026-08-16)


### Features

* **converter:** PDF gate — classify before converting, refuse scanned/mixed/sparse loudly with a machine-readable class ([#347](https://github.com/BlackForgeHQ/embookshelf/issues/347)) ([2684eb6](https://github.com/BlackForgeHQ/embookshelf/commit/2684eb6deb492d1266677e13c0d6cb71f5dc0eeb))
* **converter:** PDF gate v2 after probing — classed refusals for engine-refused verdicts, measured sparse-output gate ([#347](https://github.com/BlackForgeHQ/embookshelf/issues/347)) ([44b8861](https://github.com/BlackForgeHQ/embookshelf/commit/44b8861f0560847f96106ac977c4428cbe8ca1ea))
* **fileproc:** CBR/CB7 comic processor ([#310](https://github.com/BlackForgeHQ/embookshelf/issues/310)) ([b7154c3](https://github.com/BlackForgeHQ/embookshelf/commit/b7154c3628ae42cd5740dc7177882645665157c6))
* **fileproc:** FB2 processor ([#312](https://github.com/BlackForgeHQ/embookshelf/issues/312)) ([265f378](https://github.com/BlackForgeHQ/embookshelf/commit/265f378f987609ac750125b35abd437b9ce12962))
* **fileproc:** MOBI/AZW3 processor ([#311](https://github.com/BlackForgeHQ/embookshelf/issues/311)) ([0851693](https://github.com/BlackForgeHQ/embookshelf/commit/08516939f7fca217931d94903377f0334f899a0e))
* **opds:** paging and cross-library aggregation move into the catalog seam ([70b339d](https://github.com/BlackForgeHQ/embookshelf/commit/70b339d3a741986eb23c7c72e04a94c33292928e)), closes [#241](https://github.com/BlackForgeHQ/embookshelf/issues/241)
* **reader:** page CBR/CB7 comics in the web reader ([#329](https://github.com/BlackForgeHQ/embookshelf/issues/329)) ([d7d424f](https://github.com/BlackForgeHQ/embookshelf/commit/d7d424f6781740c5e169edbb09454d05cbf29027))


### Bug Fixes

* **ci:** go-test embeds the real UI bundle — the nosniff sweep fails without one, and has since [#330](https://github.com/BlackForgeHQ/embookshelf/issues/330) ([3fa6d4f](https://github.com/BlackForgeHQ/embookshelf/commit/3fa6d4fc3053476a1b6bccd2e89956f1f8f37dcb))
* **comics:** one page contract — a non-image page is refused on every container ([#334](https://github.com/BlackForgeHQ/embookshelf/issues/334)) ([7b1df5a](https://github.com/BlackForgeHQ/embookshelf/commit/7b1df5ac82ef4fa355cca7870df2923ca418f06f))
* **comics:** sniff page MIME when serving comic pages ([#331](https://github.com/BlackForgeHQ/embookshelf/issues/331)) ([8958a0a](https://github.com/BlackForgeHQ/embookshelf/commit/8958a0a552255e5ea74b04d388461f6e237e28a3))
* **covers:** sniff cover MIME at persist time + nosniff header ([#330](https://github.com/BlackForgeHQ/embookshelf/issues/330)) ([9d9e870](https://github.com/BlackForgeHQ/embookshelf/commit/9d9e87015b6cb188520b63eaccc9351b2b1ab599))
* **fileproc:** bound FB2 reads and drop genre persistence ([#312](https://github.com/BlackForgeHQ/embookshelf/issues/312) review) ([a12b66a](https://github.com/BlackForgeHQ/embookshelf/commit/a12b66a6460cce262dce854879de6a22588f727c))
* **fileproc:** drop the EXTH 121 cover fallback ([#311](https://github.com/BlackForgeHQ/embookshelf/issues/311) review) ([1ac1f78](https://github.com/BlackForgeHQ/embookshelf/commit/1ac1f78ab1fcbb14dd63cf2b943ea8820aa19c98))
* **fileproc:** FB2 charset decoding and cover-type sniffing ([#312](https://github.com/BlackForgeHQ/embookshelf/issues/312) final review) ([99ce800](https://github.com/BlackForgeHQ/embookshelf/commit/99ce8002390318a79563468dcb21d71938638d4a))
* **fileproc:** solid-RAR drain placement and non-ZIP comic classification ([#310](https://github.com/BlackForgeHQ/embookshelf/issues/310) review) ([52da98d](https://github.com/BlackForgeHQ/embookshelf/commit/52da98df1c54c74852c41bf9a2f9bd6953a6338c))
* **reader:** keep the comic page cache honest under concurrency ([#329](https://github.com/BlackForgeHQ/embookshelf/issues/329)) ([5ef1e51](https://github.com/BlackForgeHQ/embookshelf/commit/5ef1e51ffbdb7051e99f45f5157e8ba7d5b61e7c))
* **reader:** survive a panicking comic decoder and keep the cap physical ([#329](https://github.com/BlackForgeHQ/embookshelf/issues/329)) ([efe2770](https://github.com/BlackForgeHQ/embookshelf/commit/efe2770bdd474f9bc961a5989d54d2cac150c600))
* **scan:** snapshot the files table before the walk, not after ([#318](https://github.com/BlackForgeHQ/embookshelf/issues/318)) ([8397fd5](https://github.com/BlackForgeHQ/embookshelf/commit/8397fd51108400712b7c5bf7f3a6968c6515ab4c))
* **service:** freeDir excepts the folder asked for, not every candidate ([#323](https://github.com/BlackForgeHQ/embookshelf/issues/323)) ([bb783e2](https://github.com/BlackForgeHQ/embookshelf/commit/bb783e2285a996102dbefc951e7d0138b5023282))
* **task:** cancel check failure stops the spend instead of reading as "not cancelled" ([#333](https://github.com/BlackForgeHQ/embookshelf/issues/333)) ([225f179](https://github.com/BlackForgeHQ/embookshelf/commit/225f179286a069013bcd0f4589a6b7b1a6de64d6))
* **ui:** fluid generated covers — hero and grid sizes follow their containers ([d327024](https://github.com/BlackForgeHQ/embookshelf/commit/d327024578b06fd7d143a394fd8cb63c1cf3bca2))


### Documentation

* ADR-0036 — PDF inspection before conversion; PDF class joins the glossary ([6c93195](https://github.com/BlackForgeHQ/embookshelf/commit/6c93195bca829a8882f49cfe43b391d768b71ab9))
* **comments:** sweep stale symbol references after the [#316](https://github.com/BlackForgeHQ/embookshelf/issues/316)/[#321](https://github.com/BlackForgeHQ/embookshelf/issues/321)/[#323](https://github.com/BlackForgeHQ/embookshelf/issues/323) refactors ([d1bd172](https://github.com/BlackForgeHQ/embookshelf/commit/d1bd172715ba1e03d58bbb77e8e69aaf2568f9c5))

## [0.6.6](https://github.com/BlackForgeHQ/embookshelf/compare/v0.6.5...v0.6.6) (2026-08-11)


### Features

* **bookdrop:** S3 drop zone pulled through the local intake seam ([f48e917](https://github.com/BlackForgeHQ/embookshelf/commit/f48e9174dcc9173d11de5d49e381ac0da22fcb5f))
* **epub:** generate an EPUB from a PDF through the converter chain ([43f5383](https://github.com/BlackForgeHQ/embookshelf/commit/43f53834530b87b039af7df4c452b783e97186c5))
* **versions:** markdown rendition joins the Versions tab, downloadable ([070db5e](https://github.com/BlackForgeHQ/embookshelf/commit/070db5eaba208ad16201cf58f5b260f727839c2c))


### Bug Fixes

* **ci:** converter latest tag gate tests the version, not the tag name ([42db257](https://github.com/BlackForgeHQ/embookshelf/commit/42db25795c67a6a98d832447be3b677e8e704821))


### Documentation

* **context:** glossary entries for the [#254](https://github.com/BlackForgeHQ/embookshelf/issues/254) build stages ([a40cbad](https://github.com/BlackForgeHQ/embookshelf/commit/a40cbad8af83897dab230a1ba33b3b3f14e2d82c))
* **context:** glossary entries for the [#296](https://github.com/BlackForgeHQ/embookshelf/issues/296)–[#304](https://github.com/BlackForgeHQ/embookshelf/issues/304) seams ([81d3658](https://github.com/BlackForgeHQ/embookshelf/commit/81d365884c76bd19aac665f2c1a24ee5dd5f5230))

## [0.6.5](https://github.com/BlackForgeHQ/embookshelf/compare/v0.6.4...v0.6.5) (2026-08-09)


### Features

* **converter:** bulk conversion from the settings card, with progress ([89b7f82](https://github.com/BlackForgeHQ/embookshelf/commit/89b7f82741221a1f0ae5143015da5d422707d9e8))

## [0.6.4](https://github.com/BlackForgeHQ/embookshelf/compare/v0.6.3...v0.6.4) (2026-08-09)


### Features

* **converter:** sidecar walking skeleton — POST /convert, bytes to GFM ([735e047](https://github.com/BlackForgeHQ/embookshelf/commit/735e0472f0e4665ae3e91471be1bfe313ef1fd82)), closes [#285](https://github.com/BlackForgeHQ/embookshelf/issues/285)
* **guides:** reading guides consume Markdown renditions for PDFs ([f7581f7](https://github.com/BlackForgeHQ/embookshelf/commit/f7581f7498792f78abf156bdef982e30577a92b0)), closes [#288](https://github.com/BlackForgeHQ/embookshelf/issues/288)
* **handler:** ping the database in the healthcheck ([a7bf5cb](https://github.com/BlackForgeHQ/embookshelf/commit/a7bf5cb25e9ec03108999b8ec358178295efeaf7))
* **handler:** report commit, uptime, pool and schema on /settings/instance ([34a890e](https://github.com/BlackForgeHQ/embookshelf/commit/34a890e4fdf59a88cd3207e10765fb545e3bee0f))
* **migrator:** read the recorded schema version on demand ([43a2dbf](https://github.com/BlackForgeHQ/embookshelf/commit/43a2dbf75612023b4b35751a040d453f2c65040f))
* **renditions:** markdown rendition pipeline — job, tracking row, status API ([fb7410a](https://github.com/BlackForgeHQ/embookshelf/commit/fb7410aa59c4f60fb14e1180183fa2038ae8a9df)), closes [#287](https://github.com/BlackForgeHQ/embookshelf/issues/287)
* **service:** probe pool pressure, latency and schema version ([885c1b6](https://github.com/BlackForgeHQ/embookshelf/commit/885c1b6140cf39bc460d24efb42ccf1fc52f7f25))
* **settings:** CONVERTER row + admin panel card with live reachability ([adce343](https://github.com/BlackForgeHQ/embookshelf/commit/adce343c7df75196744afc994db8dbb68f7c4536)), closes [#286](https://github.com/BlackForgeHQ/embookshelf/issues/286)
* **ui:** About becomes an instance status board ([d609ff1](https://github.com/BlackForgeHQ/embookshelf/commit/d609ff18e9bab3cd03de554ce948c6188c4be6fd))
* **ui:** derive the instance status rows as pure functions ([27385b6](https://github.com/BlackForgeHQ/embookshelf/commit/27385b6446d0b5f78bf70105263c87f1e8c01f28))
* **ui:** type the new instance platform fields ([4d5121b](https://github.com/BlackForgeHQ/embookshelf/commit/4d5121bf8183347bd6c91bacc1452e4b73034c2a))


### Bug Fixes

* **converter:** compose healthcheck probes 127.0.0.1, not localhost ([83d14f6](https://github.com/BlackForgeHQ/embookshelf/commit/83d14f64c96762480a4af250b237328bcfdb3412))
* **guides:** late conversion failures reach the guide panel; loud hash resolution ([8486f53](https://github.com/BlackForgeHQ/embookshelf/commit/8486f5318222c87005d07e9924584d9948df9ccc))
* **migrator:** limit test DB connection pool to 1 ([a6298e3](https://github.com/BlackForgeHQ/embookshelf/commit/a6298e33b5992be6c12d92f8e372a11065c89c06))
* **ui:** Instance panel says why, not just that, the header is blank ([b6dd187](https://github.com/BlackForgeHQ/embookshelf/commit/b6dd18718e26c8d1cf141da251731dffef580921))


### Documentation

* ADR-0033 converter sidecar extension + glossary terms ([1422969](https://github.com/BlackForgeHQ/embookshelf/commit/142296923cd4d4a45383d48c1083ded69f386fbe))
* **converter:** ops guide, README coverage, prod compose service ([dd108d9](https://github.com/BlackForgeHQ/embookshelf/commit/dd108d9ea76904f481a567416d3a57a0e2d39480))
* plan the instance status panel implementation ([1c5e38d](https://github.com/BlackForgeHQ/embookshelf/commit/1c5e38d53cf648991c10aec593c3e09f591c289c))
* spec the Settings About panel as an instance status board ([23ba757](https://github.com/BlackForgeHQ/embookshelf/commit/23ba75750e5e3a875dce6fec169832e07e488326))

## [0.6.3](https://github.com/BlackForgeHQ/embookshelf/compare/v0.6.2...v0.6.3) (2026-07-30)


### Features

* **handler:** an appSettingsStore seam, and tests for the rules that had none ([f2a5aad](https://github.com/BlackForgeHQ/embookshelf/commit/f2a5aad9dfc57e4e17a3ccc295cb8c3b6889cfab)), closes [#226](https://github.com/BlackForgeHQ/embookshelf/issues/226)
* **recovery:** a subcommand finds and repairs books written outside their library ([8f4b694](https://github.com/BlackForgeHQ/embookshelf/commit/8f4b6941af08a2d7e7185afb30697ce5d8efff63)), closes [#272](https://github.com/BlackForgeHQ/embookshelf/issues/272)
* **ui:** a stored secret can be removed ([e3da33e](https://github.com/BlackForgeHQ/embookshelf/commit/e3da33ea89d6a502177407105ac36658cabbe2fb)), closes [#268](https://github.com/BlackForgeHQ/embookshelf/issues/268)


### Bug Fixes

* **app:** every background loop takes ctx, and Close waits for it ([2dfb805](https://github.com/BlackForgeHQ/embookshelf/commit/2dfb80573678c3fb1d0352b862fa9ddcd2aa74ed)), closes [#224](https://github.com/BlackForgeHQ/embookshelf/issues/224)
* **audiobook:** a mis-named engine refuses with a code, not a bare 409 ([cf473c3](https://github.com/BlackForgeHQ/embookshelf/commit/cf473c323d12aba177a97205524cae762a72ef44)), closes [#274](https://github.com/BlackForgeHQ/embookshelf/issues/274)
* **audiobook:** a run carries a generation, so a superseded job is a no-op ([c3fb386](https://github.com/BlackForgeHQ/embookshelf/commit/c3fb386769e17b650cd090eb8bb7d988a34fbc04)), closes [#253](https://github.com/BlackForgeHQ/embookshelf/issues/253)
* **audiobook:** a segment awaiting a retry is not a failed segment ([1702f72](https://github.com/BlackForgeHQ/embookshelf/commit/1702f722fc9cc6af1f2420de773e336090ae4362)), closes [#263](https://github.com/BlackForgeHQ/embookshelf/issues/263)
* **audiobook:** a zero-row segment write stops counting as success ([444411a](https://github.com/BlackForgeHQ/embookshelf/commit/444411a9672970f67d5040feb6b7b3318c2481f4)), closes [#220](https://github.com/BlackForgeHQ/embookshelf/issues/220)
* **audiobook:** an admin turning generation off reaches the client as a code ([835ffa6](https://github.com/BlackForgeHQ/embookshelf/commit/835ffa60d7ea28740f9e80f5b081e61a07cf501d)), closes [#221](https://github.com/BlackForgeHQ/embookshelf/issues/221)
* **audiobook:** deleting a narration removes its bytes ([617750e](https://github.com/BlackForgeHQ/embookshelf/commit/617750e8199b4d6d2201b3135e3bdd9d30e8c9bf)), closes [#267](https://github.com/BlackForgeHQ/embookshelf/issues/267)
* **boot:** the sibling binaries stop being partial composition roots ([e346336](https://github.com/BlackForgeHQ/embookshelf/commit/e346336802f9dd74d9a3b1a0cbe3703329e0462e)), closes [#238](https://github.com/BlackForgeHQ/embookshelf/issues/238)
* **fileproc:** CBZ reads through the storage seam like its siblings ([446284b](https://github.com/BlackForgeHQ/embookshelf/commit/446284ba180f2ae0b570ff54221a094f29152f09)), closes [#240](https://github.com/BlackForgeHQ/embookshelf/issues/240)
* **handler:** one module answers what the public origin is ([9d28c8b](https://github.com/BlackForgeHQ/embookshelf/commit/9d28c8b23dfd8d03774ac563848a58c3a0c16487)), closes [#222](https://github.com/BlackForgeHQ/embookshelf/issues/222)
* **handler:** the Send-to-Kindle enqueue guards its optional seam ([1b98314](https://github.com/BlackForgeHQ/embookshelf/commit/1b9831462bf904e5d64abccc4e3164d625ce7879)), closes [#223](https://github.com/BlackForgeHQ/embookshelf/issues/223)
* **migrate:** down reverts one migration, not every one ([88827d1](https://github.com/BlackForgeHQ/embookshelf/commit/88827d15e89bd3b88dcaf39f22c06e77eacc4af5))
* **migrate:** reverting past the first migration is not an error ([c32e9ef](https://github.com/BlackForgeHQ/embookshelf/commit/c32e9ef502d51e4205f7c8d2e15752a2c835fb07))
* **migrate:** the status writes name their ignored error ([fc68976](https://github.com/BlackForgeHQ/embookshelf/commit/fc689764cda7dfe52d12a12270c770e32d2f1b27))
* **migrator:** the SQLite down chain reverses again ([ed34361](https://github.com/BlackForgeHQ/embookshelf/commit/ed34361390e4e08588f0e44de42f0ed8ec32d523)), closes [#275](https://github.com/BlackForgeHQ/embookshelf/issues/275)
* **oidc:** the refusal contract stops lying in four places ([233fd3d](https://github.com/BlackForgeHQ/embookshelf/commit/233fd3d92ff47d565ef4d333220d76c19c176ab6)), closes [#261](https://github.com/BlackForgeHQ/embookshelf/issues/261)
* **repotest:** create pgcrypto once, under a lock ([fd9ab6e](https://github.com/BlackForgeHQ/embookshelf/commit/fd9ab6ec8eb5fcc53ae78b17a3e3157f5767e9ea))
* **review:** a broken resolve stops reading local disk, and three gaps close ([37a4119](https://github.com/BlackForgeHQ/embookshelf/commit/37a4119ad53e0768ce383c62405e3d6d6395a453))
* **review:** restore the sandbox gate, refuse a forged scheme ([feee059](https://github.com/BlackForgeHQ/embookshelf/commit/feee0598fffb1cb58baa93202621e2f35ae0fb28))
* **review:** the session queries render once, and a dead export goes ([4c4af59](https://github.com/BlackForgeHQ/embookshelf/commit/4c4af59e2dd8333bde14c7abf3b8dd4a68893547))
* **scan:** a hashless seeded row stops being flagged missing, then purged ([cdf3ebc](https://github.com/BlackForgeHQ/embookshelf/commit/cdf3ebc04e33e5b3dd77d03eb873bfc30acfed84)), closes [#264](https://github.com/BlackForgeHQ/embookshelf/issues/264)
* **service:** approve stops placing outside a migrated local library ([65e2dde](https://github.com/BlackForgeHQ/embookshelf/commit/65e2ddee750ffd263bee2338750bed1fef2b58b3)), closes [#265](https://github.com/BlackForgeHQ/embookshelf/issues/265)
* **settings:** an empty key clears an LLM or TTS key, as it already does for SMTP ([bb277d2](https://github.com/BlackForgeHQ/embookshelf/commit/bb277d25849d4accbbad19b66044ed1ad31cc08e)), closes [#218](https://github.com/BlackForgeHQ/embookshelf/issues/218)
* **settings:** EMAIL validation moves onto the row, so every caller gets it ([f029ed6](https://github.com/BlackForgeHQ/embookshelf/commit/f029ed6976f61318279db9dc8b40faed609357a6)), closes [#219](https://github.com/BlackForgeHQ/embookshelf/issues/219)
* **storage:** the library handle walks, yielding library-relative locations ([514a0f4](https://github.com/BlackForgeHQ/embookshelf/commit/514a0f459a9462d8752deb8cee7f11712499d84f)), closes [#255](https://github.com/BlackForgeHQ/embookshelf/issues/255)
* **storage:** the orphan sweeper stops deleting keys something points at ([60947b2](https://github.com/BlackForgeHQ/embookshelf/commit/60947b20dfe209c0bccd0c4d777146c9bb78a306)), closes [#273](https://github.com/BlackForgeHQ/embookshelf/issues/273)
* **test,ci:** read architecture.md by its real name; unquote the lint flag ([419968d](https://github.com/BlackForgeHQ/embookshelf/commit/419968d66a1e50da780ba644a3f82776f3a1874e))
* **ui:** bulk approve says how many rows it could not approve ([e6b1f0f](https://github.com/BlackForgeHQ/embookshelf/commit/e6b1f0f06a90a97d7ac458a8ef57ded04af5ed16)), closes [#269](https://github.com/BlackForgeHQ/embookshelf/issues/269)
* **ui:** the last three mutation failures stop being reported twice ([12dce7f](https://github.com/BlackForgeHQ/embookshelf/commit/12dce7f0d0e3f5e610f559ac20c7a85feb72d25a)), closes [#262](https://github.com/BlackForgeHQ/embookshelf/issues/262)
* **ui:** the login page names a missing email claim ([20d78e5](https://github.com/BlackForgeHQ/embookshelf/commit/20d78e56b6ba46d8c01069a4278938ae64d53b36)), closes [#261](https://github.com/BlackForgeHQ/embookshelf/issues/261)
* **ui:** the narration obstacle says which cause the server named ([a5699c1](https://github.com/BlackForgeHQ/embookshelf/commit/a5699c12716f861ed436a698c01fc5dd44abb8b3)), closes [#271](https://github.com/BlackForgeHQ/embookshelf/issues/271)


### Performance

* **storage:** the S3 adapter streams a put instead of buffering it ([338f641](https://github.com/BlackForgeHQ/embookshelf/commit/338f64165b75c074fafd736a13eb01909239698c)), closes [#266](https://github.com/BlackForgeHQ/embookshelf/issues/266)


### Documentation

* **adr:** ADR-0030 records that the absolute-row hazard is closed ([31a5dca](https://github.com/BlackForgeHQ/embookshelf/commit/31a5dca667d4cc6bbc9608dcccf88a53a2af7bba))
* **architecture:** the comic reader never cached anything ([49bd3c3](https://github.com/BlackForgeHQ/embookshelf/commit/49bd3c30695d1b8d4600aff6bd39eaaed7df97c0))
* **context:** the glossary catches up with today's seams ([c40260f](https://github.com/BlackForgeHQ/embookshelf/commit/c40260fdd9515bf8145fae2557717933f0672bea))
* **service:** the write pipeline's comments say what its code does ([cf3c12a](https://github.com/BlackForgeHQ/embookshelf/commit/cf3c12a6e9501b4c8e940c1b56274a7d8f701ec6)), closes [#225](https://github.com/BlackForgeHQ/embookshelf/issues/225)

## [0.6.2](https://github.com/BlackForgeHQ/embookshelf/compare/v0.6.1...v0.6.2) (2026-07-29)


### Features

* **jobs:** one enqueue seam, in a package both tiers can import ([dc3180c](https://github.com/BlackForgeHQ/embookshelf/commit/dc3180c89607918060ba07f3df720490640c646a))
* **storage:** folder rename is one operation with two adapters ([423605d](https://github.com/BlackForgeHQ/embookshelf/commit/423605da12a73b88f2296dcc909543c83b97b69c)), closes [#168](https://github.com/BlackForgeHQ/embookshelf/issues/168)
* **ui:** a module answers which Renditions a book has ([dcbb775](https://github.com/BlackForgeHQ/embookshelf/commit/dcbb77526dc7dc20a835573b7f646dfa3ec1c61d))
* **ui:** one module decides what an error code means for the UI ([3367245](https://github.com/BlackForgeHQ/embookshelf/commit/33672450565e120b176516e0467a4d88788462d0))


### Bug Fixes

* **audiobook:** a run that failed at finalize stops re-dispatching it ([37e9c1e](https://github.com/BlackForgeHQ/embookshelf/commit/37e9c1e258db0ff92175f3f3db6bf412751a3178)), closes [#206](https://github.com/BlackForgeHQ/embookshelf/issues/206)
* **audiobook:** an unset data path refuses instead of writing to the CWD ([85f218a](https://github.com/BlackForgeHQ/embookshelf/commit/85f218a9cf64bc383f24985db88402d887916686)), closes [#207](https://github.com/BlackForgeHQ/embookshelf/issues/207)
* **audiobook:** deleting a narration undoes everything finalize wrote ([f5a6670](https://github.com/BlackForgeHQ/embookshelf/commit/f5a6670fc65823412908a7d2d3c8af398c11eb29)), closes [#208](https://github.com/BlackForgeHQ/embookshelf/issues/208)
* **audiobook:** record a Segment and advance the run as one operation ([309c763](https://github.com/BlackForgeHQ/embookshelf/commit/309c7633e6e10c3af7dc8808b64edb91e117fc79)), closes [#157](https://github.com/BlackForgeHQ/embookshelf/issues/157)
* **audiobook:** stop discarding the detail handler's book lookup ([d7addf2](https://github.com/BlackForgeHQ/embookshelf/commit/d7addf2d9be99f31d048c08123d2d47ad1b71517)), closes [#159](https://github.com/BlackForgeHQ/embookshelf/issues/159)
* **audiobook:** the run pins the cap it was split at ([85fa198](https://github.com/BlackForgeHQ/embookshelf/commit/85fa1981a00054cd22bd56cc2c4fedd9573ab14d)), closes [#189](https://github.com/BlackForgeHQ/embookshelf/issues/189)
* **config:** resolve DATA_PATH, and give the e2e suite its preconditions ([42df0d4](https://github.com/BlackForgeHQ/embookshelf/commit/42df0d46d6ab0fa8e6f03d43b8215f37740ba7b8))
* **enrich:** apply-match silently drops a degraded write ([c310934](https://github.com/BlackForgeHQ/embookshelf/commit/c31093492ba498ef96f2efd7309e5e644748b962)), closes [#174](https://github.com/BlackForgeHQ/embookshelf/issues/174)
* **guides:** the reading guide job ignores the configured auth style ([1736849](https://github.com/BlackForgeHQ/embookshelf/commit/17368497bf796d65966869956cf219d2aa0b7d36)), closes [#156](https://github.com/BlackForgeHQ/embookshelf/issues/156)
* **jobs:** correct stale seam comments and pin BookID in dispatch tests ([d131985](https://github.com/BlackForgeHQ/embookshelf/commit/d1319856584249073bb3c13bb7333715abf2e468))
* **jobs:** restore lost doc comments and pin the audiobook queue value ([26cf037](https://github.com/BlackForgeHQ/embookshelf/commit/26cf037e36cd273383ea63b7971bc8ed4987e55d))
* **metadata:** a folder rename follows from author or title changing ([dcd81d1](https://github.com/BlackForgeHQ/embookshelf/commit/dcd81d1e9321fddd476424000b4ce64c4e6084bb)), closes [#211](https://github.com/BlackForgeHQ/embookshelf/issues/211)
* **queue:** split queue.New from Start to actually close the abandonment window ([16a4949](https://github.com/BlackForgeHQ/embookshelf/commit/16a4949c49cdf8c361d718d6b1438ee438e49cab))
* **reader:** each Rendition keeps its own resume position ([441ef88](https://github.com/BlackForgeHQ/embookshelf/commit/441ef88ced458ace2ea7f2ee92b7a24db1d54b94)), closes [#200](https://github.com/BlackForgeHQ/embookshelf/issues/200)
* **reader:** the position module owns backgrounding and the long listen ([c4ad2a6](https://github.com/BlackForgeHQ/embookshelf/commit/c4ad2a63181ab610974dc70f2e739cad641f1291)), closes [#204](https://github.com/BlackForgeHQ/embookshelf/issues/204)
* **scan:** Library scan silently skipped every S3-backed library ([cd23e77](https://github.com/BlackForgeHQ/embookshelf/commit/cd23e7708cd27dd14ff455daf5d3541f8ce92af0)), closes [#203](https://github.com/BlackForgeHQ/embookshelf/issues/203)
* **service:** the edit-side write pipeline resolves keys like every other reader ([5e49c7f](https://github.com/BlackForgeHQ/embookshelf/commit/5e49c7f1a85e4a93af29aa3d95ac1007f29da4dc)), closes [#168](https://github.com/BlackForgeHQ/embookshelf/issues/168)
* **storage:** the adapter decides whether a library is an object store ([2b6035d](https://github.com/BlackForgeHQ/embookshelf/commit/2b6035d7d7df9f92fe76eec704ffed952c8f0f7b)), closes [#202](https://github.com/BlackForgeHQ/embookshelf/issues/202)
* **task:** a drain logs to its own logger, not the process default ([e8e56c8](https://github.com/BlackForgeHQ/embookshelf/commit/e8e56c80d73aee54588b54c11df8c0b890cc04ec)), closes [#186](https://github.com/BlackForgeHQ/embookshelf/issues/186)
* **task:** close the review's fix wave on generation-worker seams ([8d23b4e](https://github.com/BlackForgeHQ/embookshelf/commit/8d23b4ecb2211d10a5cb4adf735e9af41a0b4e36))
* **task:** pin the multi-chunk unusable-audio gap and tighten assertions ([2523224](https://github.com/BlackForgeHQ/embookshelf/commit/252322414b6cb3a4643451bd517c8616867213db))
* **task:** replace unused-lint suppressions with compile-time proof ([59d0bd3](https://github.com/BlackForgeHQ/embookshelf/commit/59d0bd31b0a29d04b48672c5715fbbcf171827d4))
* **tts,queue:** unusable audio is permanent however many chunks it took ([544af03](https://github.com/BlackForgeHQ/embookshelf/commit/544af03407e17807a3cae125fbd13b34f171eb62)), closes [#185](https://github.com/BlackForgeHQ/embookshelf/issues/185)
* **tts,repo:** retire the per-request-cap seam the adapter absorbed ([c33e6f0](https://github.com/BlackForgeHQ/embookshelf/commit/c33e6f0f8aba6245ef4b5603de4c3be9c61cc67b))
* **ui:** keep one realtime connection for the whole session ([bc4c1bc](https://github.com/BlackForgeHQ/embookshelf/commit/bc4c1bc0c57cab3ad7c3b644f854bc387ccb719a)), closes [#158](https://github.com/BlackForgeHQ/embookshelf/issues/158)
* **ui:** key predicate rows by identity, and clear the Biome backlog ([bd54259](https://github.com/BlackForgeHQ/embookshelf/commit/bd54259ecbfc9e909661bcbb60957d87d08dfaa4))


### Documentation

* **adr:** the local backend stays rooted at /, and the shim stays with it ([86663a1](https://github.com/BlackForgeHQ/embookshelf/commit/86663a15c46b6f4b339e65e150283a1555dff63d)), closes [#168](https://github.com/BlackForgeHQ/embookshelf/issues/168)
* **adr:** the reader gets shared chrome, not a shared shell ([7e27d0d](https://github.com/BlackForgeHQ/embookshelf/commit/7e27d0d09d299b2078d0696d4b21feeec34bbf7b)), closes [#199](https://github.com/BlackForgeHQ/embookshelf/issues/199)
* **architecture:** register the splitter and audio fixture leaves ([1ca5790](https://github.com/BlackForgeHQ/embookshelf/commit/1ca5790f3a338498882b6bb2f1b33d6fdd43e6a1))
* **architecture:** the register describes the workers that exist ([8d024cb](https://github.com/BlackForgeHQ/embookshelf/commit/8d024cbe630865f8bc3f86126e88b181c4a423e8)), closes [#215](https://github.com/BlackForgeHQ/embookshelf/issues/215)
* **ci:** the linter pin is written once, in the Makefile ([d5d54da](https://github.com/BlackForgeHQ/embookshelf/commit/d5d54da943159bd664ee5e27468864aca564f8bb)), closes [#187](https://github.com/BlackForgeHQ/embookshelf/issues/187)
* **jobs:** restate comments and docs for the world the seam left behind ([b564d9c](https://github.com/BlackForgeHQ/embookshelf/commit/b564d9cdb177ec61fe06cf142ce5d9c603b577cb))
* **plan:** task-by-task plan for chunking in the adapter ([9bc66ee](https://github.com/BlackForgeHQ/embookshelf/commit/9bc66eefe51c768a45e5b0c490ffd78bfebb424b))
* **plan:** task-by-task plan for the generation-worker seams ([a75fd87](https://github.com/BlackForgeHQ/embookshelf/commit/a75fd877e5b5da460fa101cb8d7b26ff0e8404a9))
* **plan:** task-by-task plan for the one enqueue seam ([3a250ac](https://github.com/BlackForgeHQ/embookshelf/commit/3a250aca4847699f2f7068742ee76a6ad66eb76b))
* **spec:** agree the generation-worker seam shape for [#177](https://github.com/BlackForgeHQ/embookshelf/issues/177) ([86e10c4](https://github.com/BlackForgeHQ/embookshelf/commit/86e10c475a16a7f1c9fcf4963f38c8bb2b82adeb))
* **spec:** chunking moves inside the engine adapter ([f25d4b1](https://github.com/BlackForgeHQ/embookshelf/commit/f25d4b1adad71fcd4bb556a05ff69ee5a361b7a5))
* **spec:** one enqueue seam, and the race the holders were hiding ([87f4375](https://github.com/BlackForgeHQ/embookshelf/commit/87f4375e5c46bfa864da10b92c079f2e39bf33ee))
* the TTS caps stop being quoted, and the register lists every package ([dbd07f1](https://github.com/BlackForgeHQ/embookshelf/commit/dbd07f1de9139403a845b1a238d523772e136387)), closes [#188](https://github.com/BlackForgeHQ/embookshelf/issues/188)
* update README and add backups documentation ([7cd3767](https://github.com/BlackForgeHQ/embookshelf/commit/7cd3767daf0c5ef6ae79a55e9e7279ed7f42f29f))

## [0.6.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.6.0...v0.6.1) (2026-07-27)


### Features

* **audiobook:** generate narration from a book's EPUB ([57e6685](https://github.com/BlackForgeHQ/embookshelf/commit/57e668587c1c93726187c190cfeb397ee4f6cb48))
* **audiobook:** surface narration in the UI ([316ba52](https://github.com/BlackForgeHQ/embookshelf/commit/316ba528f7dce805725b4448ef89c7ff84ee3f8b))
* **guides:** show library coverage while a bulk run works ([fd1bcbc](https://github.com/BlackForgeHQ/embookshelf/commit/fd1bcbc4ea605302762ac0dd3fa2e47daa201f34))


### Bug Fixes

* **library:** delete a book's bytes, not just its legacy path ([c5dbc79](https://github.com/BlackForgeHQ/embookshelf/commit/c5dbc79aca9def1b315338bd42c8ec65cea8592a))
* **library:** resolve file locations against the library root ([34f0d29](https://github.com/BlackForgeHQ/embookshelf/commit/34f0d29aa966ae9f937785082f595c477d7dfc47))
* **shelves:** opening a regular shelf 500s on Postgres ([bd8b437](https://github.com/BlackForgeHQ/embookshelf/commit/bd8b43784e689a8649dd2dcc8fd671bb3cb6d449))


### Documentation

* document audiobook configuration, correct ADR-0027 on MP3 ([e327d6b](https://github.com/BlackForgeHQ/embookshelf/commit/e327d6b56db5d7d12e183572468eb6af441194c2))
* document reading guide configuration ([3b3217e](https://github.com/BlackForgeHQ/embookshelf/commit/3b3217eb83689c7e8800e993869a3bcab7a1e132))
* record the audiobook generation design ([a85ed8e](https://github.com/BlackForgeHQ/embookshelf/commit/a85ed8e90faabc07f0f146169f26663257a4af5c))

## [0.6.0](https://github.com/BlackForgeHQ/embookshelf/compare/v0.5.0...v0.6.0) (2026-07-27)


### ⚠ BREAKING CHANGES

* **auth:** changing your password now signs you out on every device, including the session you changed it from. Previously other sessions kept working. This is deliberate — a password reset is the remedy for a compromised account, and leaving an intruder's session alive defeats it — but anyone scripting against the API should expect to re-authenticate after POST /api/v1/me/password. Set-initial-password for OIDC-only accounts is unaffected: there is no old credential to invalidate.

### Features

* **audio:** centralize audio format detection and streamline related logic ([4bb6ed2](https://github.com/BlackForgeHQ/embookshelf/commit/4bb6ed21a0e57936c615592b0d665687aa3987a4))
* **errors:** implement flat error envelope with machine-readable codes ([67b06e7](https://github.com/BlackForgeHQ/embookshelf/commit/67b06e7a49aa724baa9b3d1f902d00c6a6ed2851))
* **guides:** add the book_reading_guides table and repo ([7b48733](https://github.com/BlackForgeHQ/embookshelf/commit/7b4873344389a9006f3f001aa44a41661435d2a2))
* **guides:** add the generation job and bulk run coordinator ([5f5adf0](https://github.com/BlackForgeHQ/embookshelf/commit/5f5adf052894de2601c775d0003ffadb10c2abc4))
* **guides:** add the OpenAI-compatible LLM client ([e3f12dc](https://github.com/BlackForgeHQ/embookshelf/commit/e3f12dc7f76ed60c11ecfd39ea66265492c4f799))
* **guides:** add the reading guide generator ([11eb4b2](https://github.com/BlackForgeHQ/embookshelf/commit/11eb4b20c836d33093bf8ec2bd100c542ad5e5ac))
* **guides:** add the reading guide HTTP surface ([023af3a](https://github.com/BlackForgeHQ/embookshelf/commit/023af3a4259c91a2ce9edc32a8341ecce1f9bb60))
* **guides:** add the reading guide panel and close the error-code drift ([5a77554](https://github.com/BlackForgeHQ/embookshelf/commit/5a7755412f0b34aad9429ba398c265730484184d))
* **guides:** add the reading guides settings panel ([77eda2e](https://github.com/BlackForgeHQ/embookshelf/commit/77eda2ed662b9590ef4f283cffe2bb88071f25e6))
* **guides:** add the READING_GUIDE settings row ([409ed1d](https://github.com/BlackForgeHQ/embookshelf/commit/409ed1d4b9af659ceb9116c1bec8dff9d7523dfc))
* **guides:** extract EPUB text in spine order ([2a33e5d](https://github.com/BlackForgeHQ/embookshelf/commit/2a33e5db9ce554b885203ca659b02b6dac9d3ecd))
* **guides:** support Azure endpoints and add a connection test ([a522587](https://github.com/BlackForgeHQ/embookshelf/commit/a522587d3debb18fe15206338afba00fd82a3b02))
* **import:** enhance SQLite import process with unknown table reporting and exclusion management ([2ec7aa1](https://github.com/BlackForgeHQ/embookshelf/commit/2ec7aa1a13b0eb9fd2f251706243fea08b9365a7))
* **oidc:** enhance OIDC service with diagnostics and state management ([598cd82](https://github.com/BlackForgeHQ/embookshelf/commit/598cd8284932af997d0e888e5a9c0d0e13847424))
* **provider-settings:** introduce ProviderSettingsService for managing provider configurations ([40eddf7](https://github.com/BlackForgeHQ/embookshelf/commit/40eddf768e46f070075706c4c0eb3c622011c242))
* **tests:** add comprehensive tests for column-order coupling in book and user repositories ([1d4fe9a](https://github.com/BlackForgeHQ/embookshelf/commit/1d4fe9ac35e925331551b5ed4eed556249028573))


### Bug Fixes

* **auth:** sign out every device when a password changes ([77fef62](https://github.com/BlackForgeHQ/embookshelf/commit/77fef62d76b463a6bac009aa003e4b3e1cb3e234))
* **auth:** stop a weak password from burning the reset link ([a17470b](https://github.com/BlackForgeHQ/embookshelf/commit/a17470b1955ae03d01fe56369cc1eda40a85deeb))
* **bookdrop:** take the wipe lock on the upload intake path ([d2b137d](https://github.com/BlackForgeHQ/embookshelf/commit/d2b137d2ea4b3886b77940d20f2736ef6c9bc312))
* **enrich:** degrade closed when provider settings are unreadable ([e6c9620](https://github.com/BlackForgeHQ/embookshelf/commit/e6c962058f705363963dcaabbc1bfc01a53b20d7))
* **enrich:** stop auto-enrich permanently locking every populated field ([df6d6ba](https://github.com/BlackForgeHQ/embookshelf/commit/df6d6bac998a3868e315ee2c43598d5277045247))
* **enrich:** validate cover redirects, not just the first URL ([45e65c7](https://github.com/BlackForgeHQ/embookshelf/commit/45e65c7b643ce8309108f1feee1f682a2a336b98))
* **metadata:** report writes that only reached the database ([88b047e](https://github.com/BlackForgeHQ/embookshelf/commit/88b047e1237fc9c8f6c925358bdfde8f89ab03e3))
* **reader:** sync audio play state with the media element ([b538035](https://github.com/BlackForgeHQ/embookshelf/commit/b538035633bebaff20c59e537a5919495373bd0d))
* **settings:** wire ProviderCfg into the handler ([ddb12a6](https://github.com/BlackForgeHQ/embookshelf/commit/ddb12a606de08e78ae6de66b3920543e43b0e8e6))


### Documentation

* record ADR-0024, LLM-generated reading guides ([984a600](https://github.com/BlackForgeHQ/embookshelf/commit/984a600d8805a176c1dc9808b83a01cad793b79f))

## [0.5.0](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.19...v0.5.0) (2026-07-26)


### ⚠ BREAKING CHANGES

* Postgres is now required (ADR-0023). SQLite is no longer a supported backend, and a `sqlite://` DATABASE_URL refuses to boot instead of serving.

### Features

* **database:** transition to Postgres-only backend and implement SQLite import ([09a7726](https://github.com/BlackForgeHQ/embookshelf/commit/09a7726b610d609168ff94c0785b002cf97bd859))
* **secrets:** implement at-rest encryption for settings and enhance job queue handling ([72b618b](https://github.com/BlackForgeHQ/embookshelf/commit/72b618b63734dc6dc547c27a65ead288e069f5e0))


### Chores

* mark Postgres-only removal as a breaking change ([677a1fa](https://github.com/BlackForgeHQ/embookshelf/commit/677a1fa33e0a594230a0b52d69d4022dd7f09ce3))

## [0.4.19](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.18...v0.4.19) (2026-05-09)


### Bug Fixes

* update GitHub Actions workflow to use client-id instead of app-id ([0a31357](https://github.com/BlackForgeHQ/embookshelf/commit/0a31357c816fbbb7e0794b2d237c74c1cedf2264))

## [0.4.18](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.17...v0.4.18) (2026-05-09)


### Features

* add login pending route and update routing structure ([07aaeff](https://github.com/BlackForgeHQ/embookshelf/commit/07aaeff46d1d53d3d3af14e6c4558d27e1f92b6f))


### Bug Fixes

* correct positional parameters in user update queries ([909f6bf](https://github.com/BlackForgeHQ/embookshelf/commit/909f6bf65e1ca3f268dac4155d198bc8af846106))

## [0.4.17](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.16...v0.4.17) (2026-05-08)


### Features

* enhance email subsystem with hot-reload capability and configuration updates ([29ef67b](https://github.com/BlackForgeHQ/embookshelf/commit/29ef67b2c3c5de4ce055c0f2955846508215e3c7))
* enhance README and PRD documentation with new features and improvements ([2d7c051](https://github.com/BlackForgeHQ/embookshelf/commit/2d7c05163955afefcd1b3d7742ce018cf31f5a74))
* implement forward-auth middleware for reverse-proxy header authentication ([8a78018](https://github.com/BlackForgeHQ/embookshelf/commit/8a780188b572c9c8857195b9e9ebd27248809d8f))

## [0.4.16](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.15...v0.4.16) (2026-05-08)


### Features

* add TestShelfUpdateIcon to validate SQLite and Postgres update behavior ([54b5d03](https://github.com/BlackForgeHQ/embookshelf/commit/54b5d03e6d23d28627220bc7895aeb6d1ad457fa))

## [0.4.15](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.14...v0.4.15) (2026-05-08)


### Features

* enhance SidebarMenuButton with mobile click handling ([2f43def](https://github.com/BlackForgeHQ/embookshelf/commit/2f43defa027d180902f086cde03f80b79fb3cdba))
* implement email delivery subsystem with SMTP support ([e734ce7](https://github.com/BlackForgeHQ/embookshelf/commit/e734ce76453c540f5e2c791604d4ec7602d6539e))
* implement shelf icon feature with regex validation and UI integration ([ba879fb](https://github.com/BlackForgeHQ/embookshelf/commit/ba879fb83f7f07a5fca3a56cd4605513a2c5ca50))
* remove outdated implementation plans for metadata enrichment, command search, OIDC admin approval, and SQLite support ([8a29960](https://github.com/BlackForgeHQ/embookshelf/commit/8a29960d4593e2db9fe943f90738d90d668ab770))

## [0.4.14](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.13...v0.4.14) (2026-05-07)


### Features

* enhance documentation and introduce new agent skills ([7ec8d90](https://github.com/BlackForgeHQ/embookshelf/commit/7ec8d90a3ac833d44c765ae4d4166d31873baa4b))
* introduce shared shelves feature for public curation ([7d5b001](https://github.com/BlackForgeHQ/embookshelf/commit/7d5b0018080593b5c596031a7cf825f43c2d6ee9))

## [0.4.13](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.12...v0.4.13) (2026-05-05)


### Features

* enhance ShelfCard with inline searchable picker and loading animation ([5372530](https://github.com/BlackForgeHQ/embookshelf/commit/537253092aba12d945da876d70f862b5639ff902))


### Bug Fixes

* honor sort parameter on shelf book listings ([c272958](https://github.com/BlackForgeHQ/embookshelf/commit/c2729581d86317af1f672f68d8ed64f36b595d21))

## [0.4.12](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.11...v0.4.12) (2026-05-04)


### Features

* add "Unshelved" virtual view for books not on user shelves ([e97469b](https://github.com/BlackForgeHQ/embookshelf/commit/e97469b7e9e097a6b1fb85e294aa51035505943e))

## [0.4.11](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.10...v0.4.11) (2026-05-04)


### Features

* **cover:** add endpoint and service method to remove book covers ([17aba7a](https://github.com/BlackForgeHQ/embookshelf/commit/17aba7a70597090600ad0ace4299d5cc9b06ed5e))
* **pdf-discovery:** XMP, hex strings, ISBN, client-rendered cover ([#96](https://github.com/BlackForgeHQ/embookshelf/issues/96)) ([38c9f38](https://github.com/BlackForgeHQ/embookshelf/commit/38c9f3868c4f775d3380217c76e9ee03000f5a5c))
* **pdf:** implement client-side cover rendering and enhance PDF metadata extraction ([af6005c](https://github.com/BlackForgeHQ/embookshelf/commit/af6005cfd5c6e339fdd25d47ad632522d4e7470c))


### Bug Fixes

* **bookdrop:** sanitize file names during upload to prevent issues with special characters ([bb69417](https://github.com/BlackForgeHQ/embookshelf/commit/bb69417b388a9fbe292be96d53ffcf862396b922))

## [0.4.10](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.9...v0.4.10) (2026-05-03)


### Features

* **bookdrop:** implement admin-only housekeeping operations for BookDrop ([c685fe1](https://github.com/BlackForgeHQ/embookshelf/commit/c685fe1bcabc72938250c9819a85b882229d0101))
* **cover:** add coverVersion for cache-busting on cover URLs ([6342a1f](https://github.com/BlackForgeHQ/embookshelf/commit/6342a1fa868dce5ae9a33a4427fc7a3f306d73c5))


### Documentation

* reorganize sections in PRD for clarity and consistency ([ab2ff73](https://github.com/BlackForgeHQ/embookshelf/commit/ab2ff73fada94ac6d8544334556b5d26819825cc))
* update architecture and context documentation; remove obsolete testing plan ([1610824](https://github.com/BlackForgeHQ/embookshelf/commit/1610824b832726a01169e367ef6b54f09523705f))
* update project overview and architecture details ([8474f5c](https://github.com/BlackForgeHQ/embookshelf/commit/8474f5ca001e5f8a68b5f1575cbd848e169da859))

## [0.4.9](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.8...v0.4.9) (2026-05-03)


### Features

* **auth:** add comprehensive tests for basic authentication and user context handling ([3c35b9a](https://github.com/BlackForgeHQ/embookshelf/commit/3c35b9a274b20c8bb43651dc92160c2ed0c4ddac))

## [0.4.8](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.7...v0.4.8) (2026-05-03)


### Features

* **enrichment:** implement SHA-256 hashing for book cover storage ([ab8e25b](https://github.com/BlackForgeHQ/embookshelf/commit/ab8e25b9b2a140ea469d442414691b6e119de769))
* **orphan-management:** implement pending orphans for S3 folder renames ([#86](https://github.com/BlackForgeHQ/embookshelf/issues/86)) ([3ef55a0](https://github.com/BlackForgeHQ/embookshelf/commit/3ef55a09224445d586f498c99865b4ed6f67e6a2))

## [0.4.7](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.6...v0.4.7) (2026-05-02)


### Features

* **files:** enhance book file delivery options with streaming support ([e439198](https://github.com/BlackForgeHQ/embookshelf/commit/e439198a1c8d507455f3e861f9806ab0117aefb7))

## [0.4.6](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.5...v0.4.6) (2026-05-02)


### Features

* **storage:** reconcile shared-S3 backends from env at boot ([45b82ec](https://github.com/BlackForgeHQ/embookshelf/commit/45b82ecad9d9e26df6e8298cc3b3f209fbccc553))


### Bug Fixes

* **s3:** auto-prepend https:// to scheme-less endpoint ([9734ddb](https://github.com/BlackForgeHQ/embookshelf/commit/9734ddb6e1a4911d98314e126ec59784dfeb06f2))
* **s3:** downgrade missing bucket versioning to warning ([1b5de8b](https://github.com/BlackForgeHQ/embookshelf/commit/1b5de8b03e5427ee9fb20cfd1dd2c54db7684070))

## [0.4.5](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.4...v0.4.5) (2026-05-02)


### Features

* **docker:** add S3 configuration options to production compose file ([4dde55e](https://github.com/BlackForgeHQ/embookshelf/commit/4dde55ee5c20c6be916c5131c081d3cf1b30e827))


### Bug Fixes

* **bookdrop:** improve error handling for missing placer in library approval ([e3f168f](https://github.com/BlackForgeHQ/embookshelf/commit/e3f168f4e6d9ef0eeaef8b2e87910f4ef5ab277b))
* **book:** update user_id parameter in ListMissingCoverHash query ([3edafad](https://github.com/BlackForgeHQ/embookshelf/commit/3edafad7db29f5f243f14f629ce0b4543a7242a8))

## [0.4.4](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.3...v0.4.4) (2026-05-02)


### Features

* **account:** UI for multi-provider OIDC linking (PR-2) ([187d162](https://github.com/BlackForgeHQ/embookshelf/commit/187d162a6e0b5e4043c84ea7812a52cea284eada))
* **account:** user_identities table, multi-provider OIDC linking (PR-1, backend) ([#78](https://github.com/BlackForgeHQ/embookshelf/issues/78)) ([eb3d722](https://github.com/BlackForgeHQ/embookshelf/commit/eb3d722f96b712ee8099c3ac84f65f91932572d5))

## [0.4.3](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.2...v0.4.3) (2026-05-02)


### Features

* **release:** add production build target to Makefile ([1ac1105](https://github.com/BlackForgeHQ/embookshelf/commit/1ac1105c8276ff5edd37b33d4a4fe9b9fcf838ad))

## [0.4.2](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.1...v0.4.2) (2026-05-02)


### Features

* **account:** restructure user settings into dedicated account route ([73feebf](https://github.com/BlackForgeHQ/embookshelf/commit/73feebf10c3025272116c6805ce2c95456c72554))

## [0.4.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.0...v0.4.1) (2026-05-02)


### Refactors

* **queue:** simplify ScanImport dependencies by removing unnecessary struct ([ccb885e](https://github.com/BlackForgeHQ/embookshelf/commit/ccb885e))

## [0.4.0](https://github.com/BlackForgeHQ/embookshelf/compare/v0.3.1...v0.4.0) (2026-05-02)


### ⚠ BREAKING CHANGES

* libraries.org_mode column removed; new approves and edits write the folder-per-book layout. Existing flat-layout libraries keep working but converge into the new layout lazily on user edits.

### Features

* book-per-folder library layout (ADR-0003 + ADR-0004) ([19ab3fb](https://github.com/BlackForgeHQ/embookshelf/commit/19ab3fba97444aa904b0a1bbfcc417ef71391cfd))
* **metadata:** implement MetadataWriter and effects decision logic ([bb48ea5](https://github.com/BlackForgeHQ/embookshelf/commit/bb48ea59a99cbed510d2590cee981de9b1e7ea8e))

## [0.3.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.3.0...v0.3.1) (2026-05-02)


### Features

* **fileproc:** buildIncrementalUpdate appends new /Info + xref + trailer ([a524fe0](https://github.com/BlackForgeHQ/embookshelf/commit/a524fe0d47af7f2e353251ea42b466d79a17bf95))
* **fileproc:** buildInfoBody renders /Info dict; never writes /CreationDate ([bd5b8ea](https://github.com/BlackForgeHQ/embookshelf/commit/bd5b8ea5cb1f7bc91167287b37bf6e9812d5e18f))
* **fileproc:** Embedder interface + DispatchEmbedder (EPUB only stub) ([db6a6a5](https://github.com/BlackForgeHQ/embookshelf/commit/db6a6a554bf909db9951d4ce644f018943e5fef9))
* **fileproc:** emit Calibre-compatible series meta alongside OPF 3 belongs-to-collection ([ad53cfe](https://github.com/BlackForgeHQ/embookshelf/commit/ad53cfee1a002d5e61b163116d02ceada5ca45ed))
* **fileproc:** encodePDFString emits UTF-16BE hex w/ BOM ([6f710a4](https://github.com/BlackForgeHQ/embookshelf/commit/6f710a459343b2017d8ee4a995cf87374576d2c8))
* **fileproc:** EPUBEmbedder.Embed wires mutate + rezip end-to-end ([7533204](https://github.com/BlackForgeHQ/embookshelf/commit/75332042f0ce21fe6c05602d1d20ee4b99930a15))
* **fileproc:** findInfoRef extracts existing /Info reference ([faa99a4](https://github.com/BlackForgeHQ/embookshelf/commit/faa99a4900af672e19133d54cfeacb4c09b03e6b))
* **fileproc:** findStartxref locates trailer offset ([9b34254](https://github.com/BlackForgeHQ/embookshelf/commit/9b34254364b949b80c58d129dbe8d9fd7827810c))
* **fileproc:** mutateOPF writes scalar metadata fields ([f8a989a](https://github.com/BlackForgeHQ/embookshelf/commit/f8a989adbd9537dfe33035d437d7fe3005b653ad))
* **fileproc:** nextObjectNumber returns next free object slot ([985edf1](https://github.com/BlackForgeHQ/embookshelf/commit/985edf148f132ccbafe80020e6bea48a1d8e6523))
* **fileproc:** PDFEmbedder.Embed via incremental update; register in DispatchEmbedder ([efb1590](https://github.com/BlackForgeHQ/embookshelf/commit/efb1590d11c982f40e46e57fc9487582a1826abb))
* **fileproc:** rezipEPUB copies entries verbatim, swaps OPF + cover ([e5daebc](https://github.com/BlackForgeHQ/embookshelf/commit/e5daebc9a6ef4f332ecec99f7dccfe6db687b6d5))
* **fileproc:** Tags/Genres dual write (embookshelf:* + dc:subject) ([1e8bc64](https://github.com/BlackForgeHQ/embookshelf/commit/1e8bc646ac952703cd1196e37f7ce2f66ba70c75))
* **main:** wire BookRepo + MergeLocked into LibraryScanDeps ([432a29f](https://github.com/BlackForgeHQ/embookshelf/commit/432a29f4a72d8b2baa01a84898ba7e033ca2b9bc))
* **main:** wire DataPath into LibraryServiceDeps ([339d352](https://github.com/BlackForgeHQ/embookshelf/commit/339d352fb6ecd74b7d9606437e9c5a6b01fe1298))
* **main:** wire FileRepo into MetadataWriter for hash-stamping ([1658d8e](https://github.com/BlackForgeHQ/embookshelf/commit/1658d8e06758da7adb374ff83dbed0e14e5bc869))
* **main:** wire MetadataWriter; inject into LibraryService + EnrichmentService ([c1c4984](https://github.com/BlackForgeHQ/embookshelf/commit/c1c4984c65a2cf904a5372752b9a88894f380a51))
* **metadata:** implement EditableMetadata struct and related methods ([bbd859d](https://github.com/BlackForgeHQ/embookshelf/commit/bbd859da63eb72d5fd460b8b24024f4a60fc16f9))
* **model:** EditableMetadata + Book.Editable/ApplyEditable helpers ([61dd2fa](https://github.com/BlackForgeHQ/embookshelf/commit/61dd2fac4b3766cc15ee5876f0e245d365d0ec4e))
* **repo:** introduce BookRepo for book management ([028619a](https://github.com/BlackForgeHQ/embookshelf/commit/028619a1385fda17f7c7652bf04939ce50b261e5))
* **service+handler:** ApplyMatch threads Trigger; AutoEnrich passes auto_enrichment ([0d0e691](https://github.com/BlackForgeHQ/embookshelf/commit/0d0e691225b811715b40777839cf943e71093d7e))
* **service:** implement LibraryStore for library management ([adb6f47](https://github.com/BlackForgeHQ/embookshelf/commit/adb6f47442ef11ba578cde0a8272d3a9b399fe8f))
* **service:** introduce Placer interface for bookdrop file placement ([39ebbd5](https://github.com/BlackForgeHQ/embookshelf/commit/39ebbd5efc177d9eeca8ac6a7cc407c8b70c6c41))
* **service:** LibraryHandle.SidecarKey + CanWriteInFile helpers ([311b4be](https://github.com/BlackForgeHQ/embookshelf/commit/311b4bebe79fe940f2bdc15d754f72e1565f62ee))
* **service:** LibraryService.UpdateBookMetadata routes via MetadataWriter when wired ([40e518b](https://github.com/BlackForgeHQ/embookshelf/commit/40e518b160668d1dbdc6ee5b2afcf3ec2e9ac0fa))
* **service:** managed local library folders under DATA_PATH ([c40d6e2](https://github.com/BlackForgeHQ/embookshelf/commit/c40d6e259cd7300e483fe0609e5a56c3c8f268f0))
* **service:** MergeLocked applies per-field lock-aware merge ([249ac05](https://github.com/BlackForgeHQ/embookshelf/commit/249ac0540aefb304b2f2c245c21f65b402062e90))
* **service:** MetadataWriter file embed step + spillover-mode resolution ([0f97ea2](https://github.com/BlackForgeHQ/embookshelf/commit/0f97ea2bbbc0c701f94b4bcb1049882833d2f76a))
* **service:** MetadataWriter hash-stamps files.content_hash after file write ([691f19b](https://github.com/BlackForgeHQ/embookshelf/commit/691f19bcade3c5909f612846052dfa3676d5d638))
* **service:** MetadataWriter sidecar step (manual_edit + apply_enrichment) ([3b33a3a](https://github.com/BlackForgeHQ/embookshelf/commit/3b33a3aaf8eb1215d23d9052b21fa80b665e0afe))
* **service:** MetadataWriter w/ Trigger enum; DB-only step wired ([d4a377a](https://github.com/BlackForgeHQ/embookshelf/commit/d4a377a72be4a81cf9106eb919aa97b447ae4bc2))
* **sidecar:** add JSON envelope encoder + WriteMode constants ([f18cdfd](https://github.com/BlackForgeHQ/embookshelf/commit/f18cdfd9cff75e3d1dee6cacc4225b349dd1bab7))
* **sidecar:** add KeyFor helper for paired sidecar filename ([3f237dc](https://github.com/BlackForgeHQ/embookshelf/commit/3f237dce24449c300e3ea494485110e35174d0a3))
* **sidecar:** hard-cutover TOML support; drop pelletier/go-toml dep ([6c90ca9](https://github.com/BlackForgeHQ/embookshelf/commit/6c90ca9342ba29060fd6eafbb32c7ef97f9c450a))
* **sidecar:** implement JSON sidecar cutover and embedder integration ([414ecb8](https://github.com/BlackForgeHQ/embookshelf/commit/414ecb8db5c8bd5a05d6a4fe4cf5444cb6889438))
* **sidecar:** Read takes bookKey, derives paired JSON sidecar ([1ed85f9](https://github.com/BlackForgeHQ/embookshelf/commit/1ed85f9d0d0474a0a2be2b52ee713df597d9bc0c))
* **sidecar:** switch Sidecar struct tags from toml to json ([7e4934a](https://github.com/BlackForgeHQ/embookshelf/commit/7e4934aa75fe212e02002ca9915bdbf153fc77b4))
* **sidecar:** Writer.Write emits JSON envelope (mode + format args) ([7b937ef](https://github.com/BlackForgeHQ/embookshelf/commit/7b937efdd8f942def47d25bc8598bc262a2221dd))
* **task:** library scan re-extracts + lock-aware merges on file change ([8d33989](https://github.com/BlackForgeHQ/embookshelf/commit/8d3398935c9703d4f0276f7276089f90afadb2cd))
* **ui:** implement BookDrop redesign and enhance settings panels ([47294d7](https://github.com/BlackForgeHQ/embookshelf/commit/47294d72ed00d87c0929c0904b713beb854535cb))
* **ui:** library create form drops path input; shows derived preview ([cb36f7c](https://github.com/BlackForgeHQ/embookshelf/commit/cb36f7cf33f2ea9c44bbb8caa78e0340c522964e))


### Bug Fixes

* **sidecar:** log malformed sidecar payloads instead of silently dropping ([f6913d8](https://github.com/BlackForgeHQ/embookshelf/commit/f6913d84eac254179ff937636b797211ad82f51b))
* **sidecar:** wire writerVersion via debug.BuildInfo; document DecodeJSON contract ([64d5014](https://github.com/BlackForgeHQ/embookshelf/commit/64d5014130bcecdd2e27c13dff7afe4b75e98c45))


### Documentation

* document managed local-library folders (ADR 0002) ([724b0e0](https://github.com/BlackForgeHQ/embookshelf/commit/724b0e0e0aaa5d1590de60447a5b04d5d5f025dd))
* **sidecar:** tighten readAndParse + bookdrop comments; rename test keys to .json ([aa983d2](https://github.com/BlackForgeHQ/embookshelf/commit/aa983d20234be5817d2f35dd05c8ca7d9c4d9afa))
* **spec:** update library-creation for managed-local-folder convention ([f7b222a](https://github.com/BlackForgeHQ/embookshelf/commit/f7b222a3c13ade94980dd00ed590a24704a246c9))

## [0.3.0](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.8...v0.3.0) (2026-04-30)


### ⚠ BREAKING CHANGES

* drop file-naming-patterns feature ([#72](https://github.com/BlackForgeHQ/embookshelf/issues/72))

### Features

* drop file-naming-patterns feature ([#72](https://github.com/BlackForgeHQ/embookshelf/issues/72)) ([d7936bf](https://github.com/BlackForgeHQ/embookshelf/commit/d7936bffd28f23deea14e1e4281df6a08981c519))

## [0.2.8](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.7...v0.2.8) (2026-04-30)


### Features

* S3 libraries via shared bucket ([#70](https://github.com/BlackForgeHQ/embookshelf/issues/70)) ([5331411](https://github.com/BlackForgeHQ/embookshelf/commit/53314117836cb81b50295e12986c2d83da443d7a))
* **service:** Approve uploads to S3 for s3-backed libraries ([#71](https://github.com/BlackForgeHQ/embookshelf/issues/71)) ([148086c](https://github.com/BlackForgeHQ/embookshelf/commit/148086c0edfabea2cc44e4a99ef6a92a3b6f48cf))


### Bug Fixes

* **storageloader:** resolve relative local backend root via filepath.Abs ([#66](https://github.com/BlackForgeHQ/embookshelf/issues/66)) ([243bc8e](https://github.com/BlackForgeHQ/embookshelf/commit/243bc8e540dc9fb6053ce047b8f2063bdb891c82))
* **storageloader:** root LocalFS at "/" regardless of config.root ([#68](https://github.com/BlackForgeHQ/embookshelf/issues/68)) ([abc26b1](https://github.com/BlackForgeHQ/embookshelf/commit/abc26b19d03e50eeae372ccc741894167c5e0c53))

## [0.2.7](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.6...v0.2.7) (2026-04-30)


### Features

* **coverstore:** hash-keyed cover layout + backfill (Plan E of 8) ([#59](https://github.com/BlackForgeHQ/embookshelf/issues/59)) ([ebb873d](https://github.com/BlackForgeHQ/embookshelf/commit/ebb873dbe5a75c256f5fae6e4d1d22ddfdd9988b))
* **handler:** presigned URL redirect for S3-backed books (Plan G of 8) ([#62](https://github.com/BlackForgeHQ/embookshelf/issues/62)) ([f18dd49](https://github.com/BlackForgeHQ/embookshelf/commit/f18dd49b6ba33193c6d96040ac4474845de0fbbe))
* S3 events + tier tagging (Plan H of 8) ([#63](https://github.com/BlackForgeHQ/embookshelf/issues/63)) ([71dbcca](https://github.com/BlackForgeHQ/embookshelf/commit/71dbccab4588a50401bec6c7c764974f8270cf08))
* **storage:** S3 backend + per-library resolver (Plan F of 8) ([#61](https://github.com/BlackForgeHQ/embookshelf/issues/61)) ([1ee6924](https://github.com/BlackForgeHQ/embookshelf/commit/1ee6924d36dbcd0764298b1bebcd6cda6396a93b))

## [0.2.6](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.5...v0.2.6) (2026-04-29)


### Features

* **sidecar:** metadata.opf + .embookshelf.toml read pipeline (Plan D of 8) ([#57](https://github.com/BlackForgeHQ/embookshelf/issues/57)) ([d6828b3](https://github.com/BlackForgeHQ/embookshelf/commit/d6828b31d0026f65f1d040195b787331e91c5fac))

## [0.2.5](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.4...v0.2.5) (2026-04-29)


### Features

* **scan:** two-phase scan + reconciliation (Plan C of 8) ([#55](https://github.com/BlackForgeHQ/embookshelf/issues/55)) ([364e8d1](https://github.com/BlackForgeHQ/embookshelf/commit/364e8d1156a16e7fa80094587ba5e2aa482f4c1a))

## [0.2.4](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.3...v0.2.4) (2026-04-29)


### Features

* **db:** storage_v2 schema + content-hash identity (Plan B of 8) ([#54](https://github.com/BlackForgeHQ/embookshelf/issues/54)) ([384270d](https://github.com/BlackForgeHQ/embookshelf/commit/384270debf3ed3f6b1797e68e131b8767e4e87ea))


### Documentation

* **plan:** storage v2 schema + content-hash identity (Plan B of 8) ([#51](https://github.com/BlackForgeHQ/embookshelf/issues/51)) ([5a82d4c](https://github.com/BlackForgeHQ/embookshelf/commit/5a82d4cab35ccb3b0281805e8746f0b4eba1068f))

## [0.2.3](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.2...v0.2.3) (2026-04-29)


### Features

* **storage:** backend-agnostic Storage interface (Plan A of 8) ([#50](https://github.com/BlackForgeHQ/embookshelf/issues/50)) ([32354ae](https://github.com/BlackForgeHQ/embookshelf/commit/32354ae11ee8451b5bc086b6acea5b81bf6d438f))

## [0.2.2](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.1...v0.2.2) (2026-04-29)


### Features

* add feature specifications for CI/CD, file naming patterns, library creation, metadata providers, OIDC settings, and S3 storage ([f27f336](https://github.com/BlackForgeHQ/embookshelf/commit/f27f336607157d67264edecd9fbbe98465c45c3f))
* comic (CBZ) and audiobook (MP3/M4B) readers ([#47](https://github.com/BlackForgeHQ/embookshelf/issues/47)) ([6ca0087](https://github.com/BlackForgeHQ/embookshelf/commit/6ca0087c619b1062487a685b80f7a474544ef5e1))

## [0.2.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.0...v0.2.1) (2026-04-29)


### Features

* **db:** enhance ScanStringSlice for PostgreSQL text-array literals ([5c9515f](https://github.com/BlackForgeHQ/embookshelf/commit/5c9515f0884dc8076380db82c0b0297b5a028432))

## [0.2.0](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.4...v0.2.0) (2026-04-29)


### ⚠ BREAKING CHANGES

* bare-default Postgres connections are no longer attempted. Existing deployments that already set DATABASE_URL explicitly are unaffected. Operators relying on the implicit postgres://localhost:5432/embookshelf default must now set DATABASE_URL explicitly. See README quickstart and architecture.md for the new defaults.

### Features

* SQLite backend — driver, schema, repos, FTS5 (Plan 2A of 4) ([#40](https://github.com/BlackForgeHQ/embookshelf/issues/40)) ([7256c16](https://github.com/BlackForgeHQ/embookshelf/commit/7256c167649b2c74908e9e89d59727b5d1410bc4))
* SQLite CI lanes + e2e + final docs (Plan 4 of 4) ([#43](https://github.com/BlackForgeHQ/embookshelf/issues/43)) ([0d0bbc8](https://github.com/BlackForgeHQ/embookshelf/commit/0d0bbc830f9a7f7bd3bbd4c90f8a9b2026ae49c2))
* SQLite is the default + test matrix harness (Plan 2B of 4) ([#41](https://github.com/BlackForgeHQ/embookshelf/issues/41)) ([4009de1](https://github.com/BlackForgeHQ/embookshelf/commit/4009de109d7364dba0747dd2d9a71ab8c3f2b2af))
* SQLite queue worker (Plan 3 of 4) ([#42](https://github.com/BlackForgeHQ/embookshelf/issues/42)) ([3f7d74d](https://github.com/BlackForgeHQ/embookshelf/commit/3f7d74d1a79cf9d8e782aabcedfd37059041e36e))
* **ui:** add sidebar toggle button to TopBar and enhance BookDrop layout ([4c3b1e9](https://github.com/BlackForgeHQ/embookshelf/commit/4c3b1e9bb3d78ba8a0c616967634efeab55c26ff))
* **ui:** enhance StarRating component with interactivity and rating mutation ([2235c3d](https://github.com/BlackForgeHQ/embookshelf/commit/2235c3d2d225615f15c2f2278d2263d9372d2b8f))
* **ui:** rethink edit metadata as two dedicated pages ([#44](https://github.com/BlackForgeHQ/embookshelf/issues/44)) ([f305c42](https://github.com/BlackForgeHQ/embookshelf/commit/f305c42a12bd86b9372a79a366a65de34b6ad3da))


### Bug Fixes

* **ci:** scan correct image tag for SBOM generation ([#38](https://github.com/BlackForgeHQ/embookshelf/issues/38)) ([205939f](https://github.com/BlackForgeHQ/embookshelf/commit/205939f4be5e627e9c8fd965fd4f148bc78b859c))

## [0.1.4](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.3...v0.1.4) (2026-04-27)


### Features

* OIDC admin-approval flow ([#37](https://github.com/BlackForgeHQ/embookshelf/issues/37)) ([1f662d2](https://github.com/BlackForgeHQ/embookshelf/commit/1f662d2bd0d1483139f4bcb786d7e665a9aad18c))
* update logo and manifest for embookshelf ([12eefe3](https://github.com/BlackForgeHQ/embookshelf/commit/12eefe3e47599516bcae7f8a641cad3a2726cb96))

## [0.1.3](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.2...v0.1.3) (2026-04-27)


### Features

* command-powered search palette and library combobox ([#34](https://github.com/BlackForgeHQ/embookshelf/issues/34)) ([65db034](https://github.com/BlackForgeHQ/embookshelf/commit/65db0347b36a4f3de6eefdc23ac24557696e947f))

## [0.1.2](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.1...v0.1.2) (2026-04-24)


### Features

* **docs:** add comprehensive guide for Go libraries in book metadata enrichment ([df38e20](https://github.com/BlackForgeHQ/embookshelf/commit/df38e20a36d35805d8a4e43fde757ea603842200))
* **provider:** add per-provider rate limit config to catalog ([ce0d84d](https://github.com/BlackForgeHQ/embookshelf/commit/ce0d84db4bf95e5a980a94de01b1ccbc7d7cf1f9))
* **provider:** add resilient HTTP transport with rate limiting, circuit breaking, and retries ([b57c805](https://github.com/BlackForgeHQ/embookshelf/commit/b57c8057326cd6603734aca786e754836b7b76b0))
* **provider:** add Unicode NFC normalization to match scoring ([4f952b9](https://github.com/BlackForgeHQ/embookshelf/commit/4f952b9a3acdd2146914394a29e5fcc4695e70bd))
* **provider:** resilient enrichment pipeline with per-provider rate limits, circuit breakers, retries, and Unicode scoring ([03a31cb](https://github.com/BlackForgeHQ/embookshelf/commit/03a31cbf443f4f1cf1867810371db4b1672c71e9))
* **provider:** wire per-provider resilient clients through Build() ([f40db75](https://github.com/BlackForgeHQ/embookshelf/commit/f40db75a1ffd00422d57cdb1e7ae7f0306cc643f))


### Bug Fixes

* resolve lint issues (errcheck, gofmt) ([9eae62e](https://github.com/BlackForgeHQ/embookshelf/commit/9eae62e4d21805dd5e8594ad20e07c73d8b267d6))

## [0.1.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.0...v0.1.1) (2026-04-24)


### Bug Fixes

* **ci:** commit internal/staticfs/dist/.gitkeep so embed target exists ([89dca50](https://github.com/BlackForgeHQ/embookshelf/commit/89dca5019e64ccbe8430e80ada2f7a7769846faf))
* **ui:** reconcile lockfile + tsconfig + eslint after dep bumps ([0efa9e9](https://github.com/BlackForgeHQ/embookshelf/commit/0efa9e910cfe9b5aedd312da5235dc0501e39499))

## 0.1.0 (2026-04-24)

Initial public release. Future entries on this file are managed by
[release-please](https://github.com/googleapis/release-please) based on
conventional-commit messages landed on `main`.

### Highlights at 0.1.0

* Self-hosted multi-user digital library — Go backend + React (TanStack
  Start) SPA + Postgres, shipped as a single binary with the SPA embedded
  via `//go:embed`.
* EPUB + PDF readers, full-text search, per-user shelves and annotations.
* BookDrop import queue with polling watcher, metadata enrichment across
  four providers (Google Books, Open Library, Amazon, DuckDuckGo),
  configurable file-naming patterns.
* OIDC / SSO (Google, GitHub, generic) with PKCE and admin-controlled
  provider configuration.
* OPDS 1.2 catalog for e-readers, reMarkable device sync.
* OpenTelemetry export (traces, metrics, logs) via OTLP.
* CI/CD pipeline on GitHub Actions: PR gate, path-gated Playwright e2e,
  multi-arch GHCR image publish with SBOM + SLSA provenance on tag push,
  CodeQL + Trivy + dependency-review security scans, Dependabot, and
  release-please-driven versioning.
