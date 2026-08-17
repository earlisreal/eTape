# Use Yahoo profile metadata only as optional Stock Info enrichment

Status: Accepted

Stock Info keeps Moomoo as the source of truth for its existing Industry plate
and listing exchange. It uses Yahoo's undocumented profile endpoint only for
Operating Country and Sector, and uses Yahoo Industry as a fallback when the
Moomoo Industry value is blank.

The metadata request runs outside the fundamentals path, is cached per symbol
for one day, retries failures after one hour, and hides unavailable Country or
Sector values without blocking quote or Stock Info fundamentals. The
`[stockinfo].yahoo_metadata` setting defaults on and is a kill switch for the
undocumented endpoint.

This is a deliberate trade-off: SEC EDGAR is better for incorporation country
and SIC but does not provide the requested operating Country/Sector pair;
Alpaca's Asset API has no classification fields; and licensed GICS data would
add a commercial dependency that the current Stock Info requirement does not
justify. Yahoo's endpoint may change or disappear, so the wire model keeps
Moomoo Industry precedence and treats Yahoo values as enrichment rather than a
normalized taxonomy.
