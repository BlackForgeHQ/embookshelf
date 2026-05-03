# S3 Lifecycle Setup

embookshelf tags each book file with a `tier` tag (`hot`, `warm`, or `cold`) based on how recently it was read. A bucket lifecycle rule keys transitions off that tag, moving cold objects to cheaper storage classes automatically.

## Tier definitions

| Tier | Condition | Recommended storage class |
|------|-----------|--------------------------|
| `hot` | Read within the last 90 days | S3 Standard (default) |
| `warm` | Read 91–365 days ago | S3 Standard-IA |
| `cold` | Never read, or last read > 365 days ago | S3 Glacier Instant Retrieval |

## Step 1: Apply the lifecycle rule

Save the following JSON as `lifecycle.json`:

```json
{
  "Rules": [
    {
      "Id": "embookshelf-tier-warm",
      "Status": "Enabled",
      "Filter": { "Tag": { "Key": "tier", "Value": "warm" } },
      "Transitions": [{"Days": 1, "StorageClass": "STANDARD_IA"}]
    },
    {
      "Id": "embookshelf-tier-cold",
      "Status": "Enabled",
      "Filter": { "Tag": { "Key": "tier", "Value": "cold" } },
      "Transitions": [{"Days": 1, "StorageClass": "GLACIER_IR"}]
    }
  ]
}
```

Apply it to your library bucket:

```bash
aws s3api put-bucket-lifecycle-configuration \
  --bucket <your-bucket-name> \
  --lifecycle-configuration file://lifecycle.json
```

## Step 2: Tag your books

Run `make tag` (or the compiled `embookshelf-tag` binary) to classify and tag every book file:

```bash
# Preview what would be tagged without making changes
make tag ARGS="-dry-run"

# Apply tags
make tag
```

For production cron (example with crontab):

```cron
0 2 * * * /usr/local/bin/embookshelf-tag >> /var/log/embookshelf-tag.log 2>&1
```

The tagger additionally needs:

```json
{
  "Effect": "Allow",
  "Action": ["s3:PutObjectTagging"],
  "Resource": "arn:aws:s3:::<your-bucket>/*"
}
```

## Notes

- Lifecycle transitions happen after `Days: 1` — S3 evaluates lifecycle rules once per day, so the effective minimum transition time is 1–2 days after the tag is applied.
- Retrieving a Glacier Instant Retrieval object adds latency (~milliseconds) and per-GB retrieval cost. Consider keeping `hot` books in Standard and only transitioning confirmed cold books.
- The tagger re-tags every book on every run. For very large libraries (>100k files) consider running it off-peak.
