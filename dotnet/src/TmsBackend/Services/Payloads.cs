using System.Text.Json.Serialization;

namespace TmsBackend.Services;

/// <summary>One reading published to MQTT (MQTTTemperaturePayload).</summary>
public sealed record MqttTemperaturePayload(
    [property: JsonPropertyName("probe")] string Probe,
    [property: JsonPropertyName("temp")] double Temp,
    [property: JsonPropertyName("status")] string Status,
    [property: JsonPropertyName("machinetype")] string MachineType,
    [property: JsonPropertyName("timestamp")] string Timestamp,
    [property: JsonPropertyName("minTemp")] double MinTemp,
    [property: JsonPropertyName("maxTemp")] double MaxTemp,
    [property: JsonPropertyName("ipAddress")] string IPAddress,
    [property: JsonPropertyName("probeNo")] int ProbeNo);

/// <summary>Reading sent to the Legacy API after each save (TempLogPayload).</summary>
public sealed record TempLogPayload(
    [property: JsonPropertyName("mcuId")] string McuID,
    [property: JsonPropertyName("status")] string Status,
    [property: JsonPropertyName("tempValue")] double TempValue,
    [property: JsonPropertyName("realValue")] int RealValue,
    [property: JsonPropertyName("date")] string Date,
    [property: JsonPropertyName("time")] string Time);

/// <summary>Alert / recovery / scheduled report sent to the Legacy API (AlertPayload).</summary>
public sealed record AlertPayload(
    [property: JsonPropertyName("mcuId")] string McuID,
    [property: JsonPropertyName("status")] string Status,
    [property: JsonPropertyName("tempValue")] double TempValue,
    [property: JsonPropertyName("realValue")] int RealValue,
    [property: JsonPropertyName("date")] string Date,
    [property: JsonPropertyName("time")] string Time,
    [property: JsonPropertyName("message")] string Message,
    /// <summary>"high", "low", "normal" or "report".</summary>
    [property: JsonPropertyName("alertType")] string AlertType,
    [property: JsonPropertyName("machineName")] string MachineName,
    [property: JsonPropertyName("probeNo")] int ProbeNo,
    [property: JsonPropertyName("minTemp")] double MinTemp,
    [property: JsonPropertyName("maxTemp")] double MaxTemp);

/// <summary>SSE "refresh" event after a poll-and-save cycle.</summary>
public sealed record DataSavedEvent(
    [property: JsonPropertyName("saved")] int Saved,
    [property: JsonPropertyName("errors")] int Errors);

/// <summary>SSE "temperature" event item from the alert loop.</summary>
public sealed record TemperatureUpdateEvent(
    [property: JsonPropertyName("machineName")] string MachineName,
    [property: JsonPropertyName("tempValue")] double TempValue,
    [property: JsonPropertyName("status")] string Status,
    [property: JsonPropertyName("type")] string MachineType,
    [property: JsonPropertyName("timestamp")] string Timestamp,
    [property: JsonPropertyName("minTemp")] double MinTemp,
    [property: JsonPropertyName("maxTemp")] double MaxTemp,
    [property: JsonPropertyName("ipAddress")] string IPAddress,
    [property: JsonPropertyName("probeNo")] int ProbeNo);
