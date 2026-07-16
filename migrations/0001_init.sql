-- Initial schema. duty_date is the actual duty day (Friday for weekly duties,
-- Tue/Fri for Waschküche).
CREATE TABLE schedules (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    duty_type VARCHAR(25) NOT NULL,
    duty_date DATE        NOT NULL,
    room      VARCHAR(25) NOT NULL,
    CONSTRAINT unique_duty_date UNIQUE (duty_type, duty_date)
);