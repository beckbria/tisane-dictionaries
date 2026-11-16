Build a tool which can read a Wikipedia dump and extract all of the article titles into a file, with one article title on each line.

This file will be an input to the `tisane-build` utility.

* The tool must be able to either read an already-downloaded dump OR obtain the latest pate titles dump from https://dumps.wikimedia.org/other/pagetitles/
* The tool should assume the English Wikipedia dump (`enwiki-[0-9]+-all-titles-in-ns*`) unless a different URL is explicitly provided
* The tool MUST always require explicit user confirmation before beginning any download of a mediawiki dump.
* The tool MUST download the latest revision, NOT the full history.
* The tool MUST follow the [Wikimedia User-Agent Policy](https://foundation.wikimedia.org/wiki/Policy:Wikimedia_Foundation_User-Agent_Policy).
* The tool MUST prompt the user for a URL and email address (both of which should be validated as properly formatted) and use a specific user agent referencing "TisaneDictionary/0.0 ($URL;$EMAIL)" similar to the documentation.
* The tool MUST NOT download multiple dump files in parallel and should only have a single download stream going at any one time
* The tool's output MUST include the date of the wikipedia dump in the name and SHOULD end in ".txt"
* When dumps are sharded into files, at most two files should be on the file system at any time: one which has finished downloading and is being processed, and one which is currently downloading.  After a file is processed it should be deleted to free up space.  If two files finish downloading before the first file is processed, further downloads should be held until the first file is processed and deleted.
* During development of the tool, avoid excessive downloads.  In particular, when working to test parsing, download and preserve a single copy of the dump without deleting it rather than downloading it during iterative development loops
