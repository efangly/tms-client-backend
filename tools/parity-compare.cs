// Parity check: fetches the same read-only endpoints from the Go API and the
// C# API and reports every JSON difference by path.
//
// Usage: dotnet run tools/parity-compare.cs -- <goBaseUrl> <csBaseUrl> [path ...]
//   e.g. dotnet run tools/parity-compare.cs -- http://localhost:18081 http://localhost:18080
//
// Numbers that differ by less than 1e-6 are reported separately as "float
// noise" (FLOAT columns read as 25.399999618530273 by Go vs 25.4 by C#).
using System.Text.Json;

var goBase = args.Length > 0 ? args[0] : "http://localhost:18081";
var csBase = args.Length > 1 ? args[1] : "http://localhost:18080";
var paths = args.Length > 2 ? args[2..] : DefaultPaths();

using var http = new HttpClient { Timeout = TimeSpan.FromSeconds(30) };
var failures = 0;
foreach (var path in paths)
{
    var (goStatus, goJson) = await Fetch(goBase + path);
    var (csStatus, csJson) = await Fetch(csBase + path);
    var diffs = new List<string>();
    var noise = 0;
    if (goStatus != csStatus)
        diffs.Add($"status: go={goStatus} cs={csStatus}");
    Compare("$", goJson, csJson, diffs, ref noise);

    var label = diffs.Count == 0 ? "SAME" : "DIFF";
    Console.WriteLine($"[{label}] {path} (status {goStatus}{(noise > 0 ? $", {noise} float-noise values" : "")})");
    foreach (var d in diffs.Take(15))
        Console.WriteLine("    " + d);
    if (diffs.Count > 15)
        Console.WriteLine($"    ... {diffs.Count - 15} more");
    if (diffs.Count > 0)
        failures++;
}
Console.WriteLine($"\n{paths.Length - failures}/{paths.Length} endpoints identical");
return failures == 0 ? 0 : 1;

async Task<(int, JsonElement?)> Fetch(string url)
{
    using var res = await http.GetAsync(url);
    var text = await res.Content.ReadAsStringAsync();
    try
    {
        return ((int)res.StatusCode, JsonDocument.Parse(text).RootElement.Clone());
    }
    catch (JsonException)
    {
        return ((int)res.StatusCode, null);
    }
}

static void Compare(string path, JsonElement? a, JsonElement? b, List<string> diffs, ref int noise)
{
    if (a is not { } x || b is not { } y)
    {
        if (a.HasValue != b.HasValue)
            diffs.Add($"{path}: go={(a.HasValue ? "json" : "non-json")} cs={(b.HasValue ? "json" : "non-json")}");
        return;
    }
    if (x.ValueKind != y.ValueKind)
    {
        diffs.Add($"{path}: go={Short(x)} cs={Short(y)}");
        return;
    }
    switch (x.ValueKind)
    {
        case JsonValueKind.Object:
            var xp = x.EnumerateObject().ToDictionary(p => p.Name, p => p.Value);
            var yp = y.EnumerateObject().ToDictionary(p => p.Name, p => p.Value);
            foreach (var k in xp.Keys.Union(yp.Keys))
            {
                if (!xp.ContainsKey(k)) diffs.Add($"{path}.{k}: missing in go");
                else if (!yp.ContainsKey(k)) diffs.Add($"{path}.{k}: missing in cs");
                else Compare($"{path}.{k}", xp[k], yp[k], diffs, ref noise);
            }
            break;
        case JsonValueKind.Array:
            var xa = SortByLabel(x.EnumerateArray().ToList());
            var ya = SortByLabel(y.EnumerateArray().ToList());
            if (xa.Count != ya.Count)
                diffs.Add($"{path}: length go={xa.Count} cs={ya.Count}");
            for (var i = 0; i < Math.Min(xa.Count, ya.Count); i++)
                Compare($"{path}[{i}]", xa[i], ya[i], diffs, ref noise);
            break;
        case JsonValueKind.Number:
            var dx = x.GetDouble();
            var dy = y.GetDouble();
            if (dx == dy) break;
            if (Math.Abs(dx - dy) < 1e-6) noise++;
            else diffs.Add($"{path}: go={x.GetRawText()} cs={y.GetRawText()}");
            break;
        default:
            if (x.GetRawText() != y.GetRawText())
                diffs.Add($"{path}: go={Short(x)} cs={Short(y)}");
            break;
    }
}

// Report series come from a Go map, so their order is random: compare them by label.
static List<JsonElement> SortByLabel(List<JsonElement> items) =>
    items.All(i => i.ValueKind == JsonValueKind.Object && i.TryGetProperty("label", out _))
        ? items.OrderBy(i => i.GetProperty("label").GetString(), StringComparer.Ordinal).ToList()
        : items;

static string Short(JsonElement e)
{
    var t = e.GetRawText();
    return t.Length > 80 ? t[..80] + "…" : t;
}

static string[] DefaultPaths()
{
    var today = DateTime.Now;
    string D(int daysAgo) => today.AddDays(-daysAgo).ToString("yyyy-MM-dd");
    return
    [
        "/health",
        "/api/devices",
        "/api/devices/192.168.1.111",
        "/api/devices/COM4?probeNo=1",
        "/api/devices/nope",
        "/api/machines",
        "/api/machines/192.168.1.111/1/schedule",
        "/api/machines/COM4/1/schedule",
        "/api/temp-logs",
        "/api/temp-logs?limit=5",
        $"/api/temp-logs?startDate={D(2)}&endDate={D(0)}&limit=20",
        "/api/temp-logs?devices=COM4&limit=10",
        "/api/temp-logs?devices=192.168.1.111:1,COM4&limit=10",
        "/api/temp-logs?devices=unknown",
        "/api/temp-errors",
        "/api/temp-errors?limit=5",
        "/api/archive",
        "/api/archive?startDate=2026-08-01&endDate=2026-08-31",
        $"/api/reports/templog?startDate={D(1)}&endDate={D(0)}",
        $"/api/reports/templog?startDate={D(1)}&endDate={D(0)}&devices=COM4",
        "/api/reports/templog?startDate=2026-10-01",
        "/api/reports/templog?startDate=bad&endDate=2026-10-01",
    ];
}
