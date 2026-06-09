# Flux.io

## GeoIP enrichment (MaxMind GeoLite2)

Flux.io enriches flows with country and ASN data using MaxMind's free
GeoLite2 databases. To enable this:

1. Create a free MaxMind account and generate a license key:
   https://www.maxmind.com/en/geolite2/signup
2. Download `GeoLite2-City.mmdb` and `GeoLite2-ASN.mmdb`.
3. Place both files in `./geoip/` at the repo root (this directory is
   mounted into the backend container — see `docker-compose.yml`).

If the files are absent, the backend still starts; country/ASN fields are
simply left empty and a warning is logged at startup.
