// SPDX-License-Identifier: AGPL-3.0-or-later

package opds

// Wire goldens, copied byte-for-byte from the handler-tier pins that were
// captured before feed assembly moved into this package (#319). They are
// the contract an e-reader sees; a diff here is a client-visible change.

const wantNavigationXML = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:embookshelf:opds:root</id>
  <title>embookshelf</title>
  <updated>2024-01-02T03:04:05Z</updated>
  <author>
    <name>embookshelf</name>
  </author>
  <link rel="self" href="http://example.com/opds/" type="application/atom+xml;profile=opds-catalog;kind=navigation"></link>
  <link rel="start" href="http://example.com/opds/" type="application/atom+xml;profile=opds-catalog;kind=navigation"></link>
  <link rel="search" href="http://example.com/opds/search.xml" type="application/opensearchdescription+xml"></link>
  <link rel="search" href="http://example.com/opds/search?q={searchTerms}" type="application/atom+xml;profile=opds-catalog;kind=acquisition" title="Search"></link>
  <entry>
    <id>urn:embookshelf:opds:all</id>
    <title>All books</title>
    <updated>2024-01-02T03:04:05Z</updated>
    <content type="text">Every book in the library.</content>
    <link rel="subsection" href="http://example.com/opds/all" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  </entry>
  <entry>
    <id>urn:embookshelf:opds:recent</id>
    <title>Recently added</title>
    <updated>2024-01-02T03:04:05Z</updated>
    <content type="text">Newest imports first.</content>
    <link rel="subsection" href="http://example.com/opds/recent" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  </entry>
  <entry>
    <id>urn:embookshelf:opds:library:scifi</id>
    <title>Sci-Fi</title>
    <updated>2023-01-02T03:04:05Z</updated>
    <content type="text">Library: Sci-Fi</content>
    <link rel="subsection" href="http://example.com/opds/library/scifi" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  </entry>
  <entry>
    <id>urn:embookshelf:opds:library:history</id>
    <title>History</title>
    <updated>2023-06-07T08:09:10Z</updated>
    <content type="text">Library: History</content>
    <link rel="subsection" href="http://example.com/opds/library/history" type="application/atom+xml;profile=opds-catalog;kind=acquisition"></link>
  </entry>
</feed>`

const wantOpenSearchXML = `<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>embookshelf</ShortName>
  <Description>Search the embookshelf catalog</Description>
  <Url type="application/atom+xml;profile=opds-catalog;kind=acquisition" template="http://example.com/opds/search?q={searchTerms}"></Url>
  <InputEncoding>UTF-8</InputEncoding>
</OpenSearchDescription>`
