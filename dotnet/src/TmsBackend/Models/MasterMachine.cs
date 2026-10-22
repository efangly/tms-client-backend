using System.Text.Json.Serialization;

namespace TmsBackend.Models;

/// <summary>master_machine: one row per device probe (machine_ip + probe_no).</summary>
public class MasterMachine
{
    // Non-pointer strings in Go read NULL as "" — the setters keep that behavior.

    [JsonPropertyName("machineIp")]
    public string MachineIP { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("probeNo")]
    public int ProbeNo { get; set; }

    [JsonPropertyName("probeAll")]
    public int ProbeAll { get; set; }

    [JsonPropertyName("machineName")]
    public string MachineName { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("color")]
    public string Color { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("chkOnline")]
    public string ChkOnline { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("chkSms")]
    public string ChkSms { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("chkMail")]
    public string ChkMail { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("chkMon")]
    public string ChkMon { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("chkLine")]
    public string ChkLine { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("chkReport")]
    public string ChkReport { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("minTemp")]
    public double? MinTemp { get; set; }

    [JsonPropertyName("maxTemp")]
    public double? MaxTemp { get; set; }

    [JsonPropertyName("adjTemp")]
    public double? AdjTemp { get; set; }

    /// <summary>t=Temperature, h=Humidity, p=Power.</summary>
    [JsonPropertyName("sType")]
    public string SType { get; set => field = value ?? ""; } = "";

    /// <summary>Not stored in the DB; Go always serialized it (as 0).</summary>
    [JsonPropertyName("port")]
    public int Port { get; set; }

    public double GetMinTemp() => MinTemp ?? 0;

    public double GetMaxTemp() => MaxTemp ?? 100;

    public double GetAdjTemp() => AdjTemp ?? 0;

    public bool IsTemperatureType() => SType.ToLowerInvariant() is "t" or "";

    public bool IsHumidityType() => SType.ToLowerInvariant() == "h";

    public bool IsPowerType() => SType.ToLowerInvariant() == "p";

    public string GetTypeLabel() => SType.ToLowerInvariant() switch
    {
        "h" => "Humidity",
        "p" => "Power",
        _ => "Temperature",
    };

    public string GetUnit() => SType.ToLowerInvariant() switch
    {
        "h" => "%",
        "p" => "W",
        _ => "°C",
    };

    /// <summary>The color column doubles as a comma-separated list of "HHmm" schedule times.</summary>
    public List<string> GetScheduleTimes()
    {
        if (Color == "")
            return [];
        return Color.Split(',').Select(t => t.Trim()).Where(t => t.Length == 4).ToList();
    }

    public bool HasScheduleTime(string hhmm) => GetScheduleTimes().Contains(hhmm);

    /// <summary>Shallow copy, used when a probe without its own config borrows another probe's row.</summary>
    public MasterMachine Clone() => (MasterMachine)MemberwiseClone();
}

/// <summary>A machine with its latest reading (GET /api/machines). Go embedded MasterMachine, so fields are flattened.</summary>
public sealed class MachineWithStatus : MasterMachine
{
    [JsonPropertyName("currentValue")]
    public double? CurrentValue { get; set; }

    [JsonPropertyName("lastUpdate")]
    public string? LastUpdate { get; set; }

    [JsonPropertyName("onlineStatus")]
    public string OnlineStatus { get; set; } = "";

    public static MachineWithStatus From(MasterMachine m)
    {
        var r = new MachineWithStatus();
        foreach (var p in typeof(MasterMachine).GetProperties().Where(p => p.CanWrite))
            p.SetValue(r, p.GetValue(m));
        return r;
    }
}

/// <summary>A group of probes sharing one IP (API response DTO).</summary>
public sealed class DeviceGroup
{
    [JsonPropertyName("machineIp")]
    public string MachineIP { get; set; } = "";

    [JsonPropertyName("machineName")]
    public string MachineName { get; set; } = "";

    [JsonPropertyName("probeCount")]
    public int ProbeCount { get; set; }

    [JsonPropertyName("onlineStatus")]
    public string OnlineStatus { get; set; } = "";

    [JsonPropertyName("probes")]
    public List<MachineWithStatus> Probes { get; set; } = [];
}
