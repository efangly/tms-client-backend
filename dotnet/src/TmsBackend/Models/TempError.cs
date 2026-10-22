using System.Text.Json.Serialization;

namespace TmsBackend.Models;

/// <summary>temp_error: one row per out-of-range alert.</summary>
public sealed class TempError
{
    [JsonPropertyName("machineIp")]
    public string MachineIP { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("probeNo")]
    public int ProbeNo { get; set; }

    [JsonPropertyName("machineName")]
    public string? MachineName { get; set; }

    [JsonPropertyName("tempValue")]
    public double? TempValue { get; set; }

    [JsonPropertyName("errorTime")]
    public DateTime ErrorTime { get; set; }

    [JsonPropertyName("smsStatus")]
    public int SmsStatus { get; set; }

    [JsonPropertyName("smsSendTime")]
    public DateTime? SmsSendTime { get; set; }

    [JsonPropertyName("smsSendStatus")]
    public int SmsSendStatus { get; set; }

    [JsonPropertyName("mailStatus")]
    public int MailStatus { get; set; }

    [JsonPropertyName("mailSendTime")]
    public DateTime? MailSendTime { get; set; }

    [JsonPropertyName("mailSendStatus")]
    public int MailSendStatus { get; set; }

    [JsonPropertyName("lineStatus")]
    public int LineStatus { get; set; }

    [JsonPropertyName("lineSendTime")]
    public DateTime? LineSendTime { get; set; }

    [JsonPropertyName("lineSendStatus")]
    public int LineSendStatus { get; set; }

    [JsonPropertyName("minTemp")]
    public double? MinTemp { get; set; }

    [JsonPropertyName("maxTemp")]
    public double? MaxTemp { get; set; }

    [JsonPropertyName("monStatus")]
    public int MonStatus { get; set; }

    [JsonPropertyName("mailCount")]
    public int MailCount { get; set; }

    [JsonPropertyName("smsCount")]
    public int SmsCount { get; set; }

    [JsonPropertyName("lineCount")]
    public int LineCount { get; set; }

    [JsonPropertyName("sType")]
    public string SType { get; set => field = value ?? ""; } = "";

    /// <summary>p=process, f=finish.</summary>
    [JsonPropertyName("tempStatus")]
    public string TempStatus { get; set => field = value ?? ""; } = "";

    /// <summary>o=Over, n=Normal.</summary>
    [JsonPropertyName("errorType")]
    public string ErrorType { get; set => field = value ?? ""; } = "";
}
