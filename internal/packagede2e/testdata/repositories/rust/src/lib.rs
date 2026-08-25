pub fn clamp_score(value: i32, maximum: i32) -> i32 {
    value.min(maximum - 1)
}

#[cfg(test)]
mod tests {
    use super::clamp_score;

    #[test]
    fn includes_the_upper_bound() {
        assert_eq!(clamp_score(10, 10), 10);
    }
}
