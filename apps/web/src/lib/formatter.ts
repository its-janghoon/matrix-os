import dayjs from "dayjs";

export const formatUnderscore = (text: string): string => {
  const formatted = text.split("_").join(" ");
  return formatted.replace(/\w\S*/g, function (txt) {
    return txt.charAt(0).toUpperCase() + txt.substr(1).toLowerCase();
  });
};

export const renderChatDate = (timestamp: string | Date) => {
  const today = dayjs();
  const chatTime = dayjs(timestamp);

  if (chatTime.isBefore(today, "day")) {
    return chatTime.format("L");
  } else {
    return chatTime.format("LT");
  }
};

export const formatDeadline = (startDate: string | Date) => {
  const diffDays = dayjs(startDate).diff(dayjs(), "days");
  return diffDays;
};
