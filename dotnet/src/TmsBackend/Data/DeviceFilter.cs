using Dapper;
using TmsBackend.Models;

namespace TmsBackend.Data;

/// <summary>
/// SQL condition for the "devices" query param (applyDeviceFilter in
/// handlers.go): comma-separated tokens "ident" or "ident:probeNo", where
/// ident is a machine_ip or machine_name. A bare ident matches every probe of
/// that device; "ident:probeNo" narrows to one probe.
/// </summary>
public sealed record DeviceFilter(IReadOnlyList<string> Ips, IReadOnlyList<(string Ip, int ProbeNo)> Pairs)
{
    public bool MatchesNothing => Ips.Count == 0 && Pairs.Count == 0;

    public static DeviceFilter Parse(string devicesParam, IReadOnlyList<MasterMachine> allMachines)
    {
        var ips = new List<string>();
        var pairs = new List<(string, int)>();

        // Go's splitComma: no trimming, empty string yields no tokens.
        var tokens = devicesParam == "" ? [] : devicesParam.Split(',');
        foreach (var token in tokens)
        {
            var colon = token.IndexOf(':');
            var ident = colon >= 0 ? token[..colon] : token;
            var probeNo = 0;
            // Skip malformed "ident:probeNo" tokens, as strconv.Atoi failing does in Go.
            if (colon >= 0 && !int.TryParse(token[(colon + 1)..], System.Globalization.NumberStyles.AllowLeadingSign, System.Globalization.CultureInfo.InvariantCulture, out probeNo))
                continue;

            foreach (var m in allMachines)
            {
                if (m.MachineIP != ident && m.MachineName != ident)
                    continue;
                if (colon >= 0)
                    pairs.Add((m.MachineIP, probeNo));
                else if (!ips.Contains(m.MachineIP))
                    ips.Add(m.MachineIP);
            }
        }
        return new DeviceFilter(ips, pairs);
    }

    /// <summary>A parenthesized WHERE fragment; parameters are added to <paramref name="p"/>.</summary>
    public string ToSql(DynamicParameters p)
    {
        // No identifiers resolved (empty or entirely invalid filter): match nothing.
        if (MatchesNothing)
            return "1 = 0";

        var clauses = new List<string>();
        if (Ips.Count > 0)
        {
            clauses.Add("machine_ip IN @devIps");
            p.Add("devIps", Ips);
        }
        for (var i = 0; i < Pairs.Count; i++)
        {
            clauses.Add($"(machine_ip = @devIp{i} AND probe_no = @devProbe{i})");
            p.Add($"devIp{i}", Pairs[i].Ip);
            p.Add($"devProbe{i}", Pairs[i].ProbeNo);
        }
        return "(" + string.Join(" OR ", clauses) + ")";
    }
}
