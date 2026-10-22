using System.Text.Json.Serialization;

namespace TmsBackend.Models;

/// <summary>
/// temp_log row. temp_log_archive has the same shape, so this type is used for
/// both tables and for the JSON archive files.
/// </summary>
public sealed class TempLog
{
    [JsonPropertyName("machineIp")]
    public string MachineIP { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("probeNo")]
    public int ProbeNo { get; set; }

    [JsonPropertyName("mcuId")]
    public string? McuID { get; set; }

    [JsonPropertyName("tempValue")]
    public double? TempValue { get; set; }

    [JsonPropertyName("realValue")]
    public int? RealValue { get; set; }

    [JsonPropertyName("status")]
    public string? Status { get; set; }

    [JsonPropertyName("sendTime")]
    public DateTime? SendTime { get; set; }

    [JsonPropertyName("insertTime")]
    public DateTime InsertTime { get; set; }

    [JsonPropertyName("sDate")]
    public string? SDate { get; set; }

    [JsonPropertyName("sTime")]
    public string? STime { get; set; }
}
